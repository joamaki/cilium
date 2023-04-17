// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build linux

package linux

import (
	"context"
	"net/netip"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
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
	testutils.PrivilegedTest(t)
	defer devicesControllerTestSetup(t)

	// The test steps perform an action, wait for devices table to change
	// and then validate the change. If t.Fail is called from either
	// act or check the test stops.
	testSteps := []struct {
		step  string
		act   func(*testing.T)
		check func(*testing.T, []*tables.Device)
	}{
		{
			"initial",
			func(*testing.T) {},
			func(t *testing.T, devs []*tables.Device) {
				if assert.Len(t, devs, 1) {
					assert.Equal(t, "dummy0", devs[0].Name)
					assert.True(t, devs[0].Index > 0)
					assert.True(t, devs[0].Viable)
				}
			},
		},
		{
			"add dummy1",
			func(t *testing.T) {
				// Create another dummy to check that the table updates.
				assert.NoError(t, createDummy("dummy1", "192.168.1.1/24", false))
			},
			func(t *testing.T, devs []*tables.Device) {
				if assert.Len(t, devs, 2) {
					// Since we're indexing by ifindex, we expect these to be in the order
					// they were added.
					assert.Equal(t, "dummy0", devs[0].Name)
					assert.Equal(t, "dummy1", devs[1].Name)
					assert.True(t, devs[1].Viable)
				}
			},
		},

		{ // Only consider veth devices when they have a default route.
			"veth-without-default-gw",
			func(t *testing.T) {
				assert.NoError(t, createVeth("veth0", "192.168.4.1/24", false))
			},
			func(t *testing.T, devs []*tables.Device) {
				if assert.Len(t, devs, 4) {
					if assert.Equal(t, "veth0_", devs[2].Name) {
						assert.False(t, devs[2].Viable)
					}
					if assert.Equal(t, "veth0", devs[3].Name) {
						assert.False(t, devs[3].Viable, "veth0 without default gateway should not be viable")
					}
				}
			},
		},

		{
			"delete-dummy0",
			func(t *testing.T) {
				assert.NoError(t, deleteLink("dummy0"))
			},
			func(t *testing.T, devs []*tables.Device) {
				if assert.Len(t, devs, 3) {
					assert.Equal(t, "dummy1", devs[0].Name)
					if assert.Len(t, devs[0].Addrs, 1) {
						assert.Equal(t, devs[0].Addrs[0].Addr, netip.MustParseAddr("192.168.1.1"))
					}
				}
			},
		},

		{
			"veth-with-default-gw",
			func(t *testing.T) {
				assert.NoError(t,
					addRoute(addRouteParams{iface: "veth0", gw: "192.168.4.254", table: unix.RT_TABLE_MAIN}))
			},
			func(t *testing.T, devs []*tables.Device) {
				if assert.Len(t, devs, 3) {
					assert.Equal(t, "dummy1", devs[0].Name)
					assert.True(t, devs[0].Viable)
					assert.Equal(t, "veth0_", devs[1].Name)
					assert.False(t, devs[1].Viable)
					assert.Equal(t, "veth0", devs[2].Name)
					if assert.Len(t, devs[2].Addrs, 1) {
						assert.Equal(t, devs[2].Addrs[0].Addr, netip.MustParseAddr("192.168.4.1"))
					}
					assert.True(t, devs[2].Viable)
					assert.NoError(t, deleteLink("veth0"))
				}
			},
		},
		{
			"bond-is-viable",
			func(t *testing.T) {
				assert.NoError(t, createBond("bond0", "192.168.6.1/24", false))
				assert.NoError(t, setBondMaster("dummy1", "bond0"))
			},
			func(t *testing.T, devs []*tables.Device) {
				if assert.Len(t, devs, 2) {
					assert.Equal(t, "dummy1", devs[0].Name)
					assert.False(t, devs[0].Viable, "bonded dummy1 should not be viable")
					assert.Equal(t, "bond0", devs[1].Name)
					assert.True(t, devs[1].Viable, "bond device should be viable")
				}
				assert.NoError(t, deleteLink("bond0"))
			},
		},
		{
			"skip-bridge-devices",
			func(t *testing.T) {
				assert.NoError(t, createBridge("br0", "192.168.5.1/24", false))
				assert.NoError(t, setMaster("dummy1", "br0"))
			},
			func(t *testing.T, devs []*tables.Device) {
				if assert.Len(t, devs, 1) {
					assert.Equal(t, "dummy1", devs[0].Name)
					assert.False(t, devs[0].Viable, "bridged dummy1 should not be viable")
				}
				assert.NoError(t, deleteLink("br0"))
			},
		},
	}

	withFreshNetNS(t, func(ns netns.NsHandle) {

		var (
			db           statedb.DB
			devicesTable statedb.Table[*tables.Device]
		)
		h := hive.New(
			statedb.Cell,
			tables.Cell,
			DevicesControllerCell,
			cell.Provide(func() *netns.NsHandle { return &ns }),

			cell.Provide(func() DevicesConfig {
				return DevicesConfig{}
			}),

			cell.Invoke(func(db_ statedb.DB, devicesTable_ statedb.Table[*tables.Device]) {
				db = db_
				devicesTable = devicesTable_
			}))

		// Create a dummy device that we should see right after start.
		assert.NoError(t, createDummy("dummy0", "192.168.0.1/24", false))

		err := h.Start(context.TODO())
		assert.NoError(t, err)

		var invalidated <-chan struct{} = nil

		for _, step := range testSteps {
			step.act(t)
			if t.Failed() {
				break
			}
			// Wait for table to change.
			if invalidated != nil {
				<-invalidated
			}

			// Get the new set of devices
			devices := devicesTable.Reader(db.ReadTxn())
			it, err := devices.Get(statedb.All)
			assert.NoError(t, err)
			invalidated = it.Invalidated()

			devs := statedb.Collect[*tables.Device](it)
			step.check(t, devs)
			if t.Failed() {
				break
			}
		}

		err = h.Stop(context.TODO())
		assert.NoError(t, err)
	})
}

// Test that if the user specifies a device wildcard, then all devices not matching the wildcard
// will be marked as non-viable.
func TestDevicesController_Wildcards(t *testing.T) {
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

		err := h.Start(context.TODO())
		assert.NoError(t, err)

		getDevices := func() (devs []*tables.Device, invalidated <-chan struct{}) {
			devices := devicesTable.Reader(db.ReadTxn())
			it, err := devices.Get(statedb.All)
			assert.NoError(t, err)
			return statedb.Collect[*tables.Device](it), it.Invalidated()
		}

		_, invalidated := getDevices()

		assert.NoError(t, createDummy("dummy0", "192.168.0.1/24", false))
		assert.NoError(t, createDummy("nonviable", "192.168.1.1/24", false))

		<-invalidated
		devs, _ := getDevices()
		if assert.Len(t, devs, 1) {
			assert.Equal(t, "dummy0", devs[0].Name)
			assert.True(t, devs[0].Viable)
		}

		err = h.Stop(context.TODO())
		assert.NoError(t, err)
	})
}
