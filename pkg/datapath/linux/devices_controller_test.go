// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build linux

package linux

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"github.com/vishvananda/netns"
	"go.uber.org/goleak"
	"golang.org/x/sys/unix"

	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/testutils"
)

func withFreshNetNS(t *testing.T, test func(netns.NsHandle)) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	oldNetNS, err := netns.Get()
	assert.NoError(t, err)
	testNetNS, err := netns.New()
	assert.NoError(t, err)
	defer func() { assert.NoError(t, testNetNS.Close()) }()
	defer func() { assert.NoError(t, netns.Set(oldNetNS)) }()
	test(testNetNS)
}

func devicesControllerTestSetup(t *testing.T) func() {
	logging.SetLogLevelToDebug()
	// Return the defer function.
	return func() {
		goleak.VerifyNone(
			t,
			goleak.IgnoreCurrent(),
			// Ignore loop() and the netlink goroutines. These are left behind as netlink library has a bug
			// that causes it to be stuck in Recvfrom even after stop channel closes.
			// This is fixed by https://github.com/vishvananda/netlink/pull/793, but that has not been merged.
			// These goroutines will terminate after any route or address update.
			goleak.IgnoreTopFunction("github.com/cilium/cilium/pkg/datapath/linux.(*devicesController).loop"),
			goleak.IgnoreTopFunction("syscall.Syscall6"), // Recvfrom
		)
	}
}

func TestDevicesController(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	testutils.PrivilegedTest(t)
	defer devicesControllerTestSetup(t)

	routeExists := func(routes []*tables.Route, linkName, dst, src string) bool {
		for _, r := range routes {
			if r.LinkName == linkName && r.Dst.String() == dst && r.Src.String() == src {
				return true
			}
		}
		return false
	}

	// The test steps perform an action, wait for devices table to change
	// and then validate the change. Since we may see intermediate states
	// in the devices table (as there's multiple netlink updates that may
	// be processed at different times) the check function is repeated
	// until the desired state is reached or [ctx] times out.
	testSteps := []struct {
		name  string
		act   func(*testing.T)
		check func(*testing.T, []*tables.Device, []*tables.Route) bool
	}{
		{
			"initial",
			func(*testing.T) {},
			func(t *testing.T, devs []*tables.Device, routes []*tables.Route) bool {
				return len(devs) == 1 &&
					devs[0].Name == "dummy0" &&
					devs[0].Index > 0 &&
					devs[0].Viable &&
					routeExists(routes, "dummy0", "192.168.0.0/24", "192.168.0.1")
			},
		},
		{
			"add dummy1",
			func(t *testing.T) {
				// Create another dummy to check that the table updates.
				assert.NoError(t, createDummy("dummy1", "192.168.1.1/24", false))
			},
			func(t *testing.T, devs []*tables.Device, routes []*tables.Route) bool {
				// Since we're indexing by ifindex, we expect these to be in the order
				// they were added.
				return len(devs) == 2 &&
					"dummy0" == devs[0].Name &&
					routeExists(routes, "dummy0", "192.168.0.0/24", "192.168.0.1") &&
					devs[0].Viable &&
					"dummy1" == devs[1].Name &&
					devs[1].Viable &&
					routeExists(routes, "dummy1", "192.168.1.0/24", "192.168.1.1")
			},
		},

		{ // Only consider veth devices when they have a default route.
			"veth-without-default-gw",
			func(t *testing.T) {
				assert.NoError(t, createVeth("veth0", "192.168.4.1/24", false))
			},
			func(t *testing.T, devs []*tables.Device, routes []*tables.Route) bool {
				return len(devs) == 4 &&
					"veth0_" == devs[2].Name &&
					!devs[2].Viable &&
					"veth0" == devs[3].Name &&
					!devs[3].Viable
			},
		},

		{
			"delete-dummy0",
			func(t *testing.T) {
				assert.NoError(t, deleteLink("dummy0"))
			},
			func(t *testing.T, devs []*tables.Device, routes []*tables.Route) bool {
				return len(devs) == 3 &&
					"dummy1" == devs[0].Name &&
					len(devs[0].Addrs) == 1 &&
					devs[0].Addrs[0].Addr == netip.MustParseAddr("192.168.1.1")
			},
		},

		{
			"veth-with-default-gw",
			func(t *testing.T) {
				assert.NoError(t,
					addRoute(addRouteParams{iface: "veth0", gw: "192.168.4.254", table: unix.RT_TABLE_MAIN}))
			},
			func(t *testing.T, devs []*tables.Device, routes []*tables.Route) bool {
				return len(devs) == 3 &&
					devs[0].Name == "dummy1" &&
					devs[0].Viable &&
					devs[1].Name == "veth0_" &&
					!devs[1].Viable &&
					devs[2].Name == "veth0" &&
					len(devs[2].Addrs) == 1 &&
					devs[2].Addrs[0].Addr == netip.MustParseAddr("192.168.4.1") &&
					devs[2].Viable
			},
		},
		{
			"bond-is-viable",
			func(t *testing.T) {
				assert.NoError(t, deleteLink("veth0"))
				assert.NoError(t, createBond("bond0", "192.168.6.1/24", false))
				assert.NoError(t, setBondMaster("dummy1", "bond0"))
			},
			func(t *testing.T, devs []*tables.Device, routes []*tables.Route) bool {
				return len(devs) == 2 &&
					devs[0].Name == "dummy1" &&
					!devs[0].Viable &&
					devs[1].Name == "bond0" &&
					devs[1].Viable
			},
		},
		{
			"skip-bridge-devices",
			func(t *testing.T) {
				assert.NoError(t, deleteLink("bond0"))
				assert.NoError(t, createBridge("br0", "192.168.5.1/24", false))
				assert.NoError(t, setMaster("dummy1", "br0"))
			},
			func(t *testing.T, devs []*tables.Device, routes []*tables.Route) bool {
				return len(devs) == 1 &&
					devs[0].Name == "dummy1" &&
					!devs[0].Viable
			},
		},
	}

	withFreshNetNS(t, func(ns netns.NsHandle) {

		var (
			db           statedb.DB
			devicesTable statedb.Table[*tables.Device]
			routesTable  statedb.Table[*tables.Route]
		)
		h := hive.New(
			statedb.Cell,
			tables.Cell,
			DevicesControllerCell,
			cell.Provide(func() *netns.NsHandle { return &ns }),

			cell.Provide(func() DevicesConfig {
				return DevicesConfig{}
			}),

			cell.Invoke(func(db_ statedb.DB, devicesTable_ statedb.Table[*tables.Device], routesTable_ statedb.Table[*tables.Route]) {
				db = db_
				devicesTable = devicesTable_
				routesTable = routesTable_
			}))

		// Create a dummy device before starting to exercise initialize()
		require.NoError(t, createDummy("dummy0", "192.168.0.1/24", false))

		err := h.Start(ctx)
		require.NoError(t, err)

		for _, step := range testSteps {
			step.act(t)
			if t.Failed() {
				break
			}

			// Get the new set of devices
			for {
				txn := db.ReadTxn()
				devsIter, err := devicesTable.Reader(txn).Get(statedb.All)
				require.NoError(t, err)
				devs := statedb.Collect[*tables.Device](devsIter)

				routesIter, err := routesTable.Reader(txn).Get(statedb.All)
				require.NoError(t, err)
				routes := statedb.Collect[*tables.Route](routesIter)

				if step.check(t, devs, routes) {
					break
				}

				// Wait for a changes and try again.
				select {
				case <-routesIter.Invalidated():
				case <-devsIter.Invalidated():
				case <-ctx.Done():
					t.Fatalf("Test %s timed out while waiting for devices. Last devices seen: %+v", step.name, devs)
				}
			}

			if t.Failed() {
				break
			}
		}

		err = h.Stop(ctx)
		require.NoError(t, err)
	})
}

// Test that if the user specifies a device wildcard, then all devices not matching the wildcard
// will be marked as non-viable.
func TestDevicesController_Wildcards(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	testutils.PrivilegedTest(t)
	defer devicesControllerTestSetup(t)

	withFreshNetNS(t, func(ns netns.NsHandle) {

		var (
			db           statedb.DB
			devicesTable statedb.Table[*tables.Device]
		)
		h := hive.New(
			statedb.Cell,
			tables.Cell,
			DevicesControllerCell,
			cell.Provide(func() DevicesConfig {
				return DevicesConfig{
					Devices: []string{"dummy+"},
				}
			}),
			cell.Provide(func() *netns.NsHandle { return &ns }),
			cell.Invoke(func(db_ statedb.DB, devicesTable_ statedb.Table[*tables.Device]) {
				db = db_
				devicesTable = devicesTable_
			}))

		err := h.Start(ctx)
		assert.NoError(t, err)

		getDevices := func() (devs []*tables.Device, invalidated <-chan struct{}) {
			devices := devicesTable.Reader(db.ReadTxn())
			it, err := devices.Get(statedb.All)
			assert.NoError(t, err)
			return statedb.Collect[*tables.Device](it), it.Invalidated()
		}

		assert.NoError(t, createDummy("dummy0", "192.168.0.1/24", false))
		assert.NoError(t, createDummy("nonviable", "192.168.1.1/24", false))

		for {
			devs, invalidated := getDevices()
			if len(devs) == 1 && devs[0].Name == "dummy0" && devs[0].Viable {
				break
			}

			// Not yet what we expected, wait for changes and try again.
			select {
			case <-ctx.Done():
				t.Fatalf("Test timed out while waiting for devices")
			case <-invalidated:
			}
		}

		err = h.Stop(context.TODO())
		assert.NoError(t, err)
	})
}

type testHandle struct {
	getLinks func() []netlink.Link
}

// AddrList implements netlinkHandle.
func (testHandle) AddrList(link netlink.Link, family int) ([]netlink.Addr, error) {
	return nil, nil
}

// Close implements netlinkHandle.
func (testHandle) Close() {
}

// LinkList implements netlinkHandle.
func (t testHandle) LinkList() ([]netlink.Link, error) {
	return t.getLinks(), nil
}

// RouteListFiltered implements netlinkHandle.
func (testHandle) RouteListFiltered(family int, filter *netlink.Route, filterMask uint64) ([]netlink.Route, error) {
	return nil, nil
}

var _ netlinkHandle = testHandle{}

func TestDevicesController_Restarts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var (
		db           statedb.DB
		devicesTable statedb.Table[*tables.Device]
	)

	logging.SetLogLevelToDebug()

	// Is this the first subscription?
	var first atomic.Bool
	first.Store(true)

	funcs := netlinkFuncs{
		RouteSubscribe: func(ch chan<- netlink.RouteUpdate, done <-chan struct{}, errorCallback func(error)) error {
			go func() {
				defer close(ch)
				if !first.Load() {
					_, ipn, _ := net.ParseCIDR("1.2.3.0/24")
					select {
					case <-done:
					case ch <- netlink.RouteUpdate{
						Type: unix.RTM_NEWROUTE,
						Route: netlink.Route{
							LinkIndex: 1,
							Table:     unix.RT_TABLE_DEFAULT,
							Scope:     unix.RT_SCOPE_SITE,
							Dst:       ipn,
						},
					}:
					}
				}
				<-done
			}()
			return nil
		},
		AddrSubscribe: func(ch chan<- netlink.AddrUpdate, done <-chan struct{}, errorCallback func(error)) error {
			go func() {
				defer close(ch)
				if !first.Load() {
					_, ipn, _ := net.ParseCIDR("1.2.3.4/24")
					select {
					case <-done:
					case ch <- netlink.AddrUpdate{
						LinkAddress: *ipn,
						LinkIndex:   1,
						NewAddr:     true,
					}:
					}
				}
				<-done
			}()
			return nil
		},
		LinkSubscribe: func(ch chan<- netlink.LinkUpdate, done <-chan struct{}, errorCallback func(error)) error {
			go func() {
				defer close(ch)
				if first.Load() {
					// Simulate a netlink socket failure on the first subscription round
					errorCallback(errors.New("first"))
					first.Store(false)
				} else {

					select {
					case <-done:
					case ch <- netlink.LinkUpdate{
						IfInfomsg: nl.IfInfomsg{IfInfomsg: unix.IfInfomsg{Index: 1}},
						Header:    unix.NlMsghdr{Type: unix.RTM_NEWLINK},
						Link: &netlink.Dummy{
							LinkAttrs: netlink.LinkAttrs{
								Index:        1,
								Name:         "dummy",
								HardwareAddr: []byte{1, 2, 3, 4, 5, 6},
							},
						},
					}:
					}
				}
				<-done
			}()
			return nil
		},
	}

	handle := testHandle{
		getLinks: func() []netlink.Link {
			if first.Load() {
				// On first round we create a stale device that should get flushed
				// from the devices table.
				return []netlink.Link{&netlink.Dummy{
					LinkAttrs: netlink.LinkAttrs{
						Index:        2,
						Name:         "stale",
						HardwareAddr: []byte{2, 3, 4, 5, 6, 7},
					},
				}}
			}
			return nil
		},
	}

	h := hive.New(
		statedb.Cell,
		tables.Cell,
		DevicesControllerCell,
		cell.Provide(func() DevicesConfig {
			return DevicesConfig{}
		}),
		cell.Provide(
			func() (*netlinkFuncs, netlinkHandle) {
				return &funcs, handle
			},
		),
		cell.Invoke(func(db_ statedb.DB, devicesTable_ statedb.Table[*tables.Device]) {
			db = db_
			devicesTable = devicesTable_
		}))

	err := h.Start(ctx)
	assert.NoError(t, err)

	// Wait for the dummy device to appear.
	var devs []*tables.Device
	for {
		devices := devicesTable.Reader(db.ReadTxn())
		it, err := devices.Get(statedb.All)
		assert.NoError(t, err)
		devs = statedb.Collect[*tables.Device](it)

		if len(devs) != 0 && devs[0].Name == "dummy" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Test timed out while waiting for device")
		case <-it.Invalidated():
			// Query again
		}
	}

	if assert.Len(t, devs, 1) {
		assert.Equal(t, "dummy", devs[0].Name)
		assert.True(t, devs[0].Viable)
	}

	err = h.Stop(ctx)
	assert.NoError(t, err)

}
