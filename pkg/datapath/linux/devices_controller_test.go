// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build linux

package linux

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vishvananda/netns"
	"go.uber.org/goleak"

	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/testutils"
)

func withFreshNetNS(t *testing.T, test func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	oldNetNS, err := netns.Get()
	assert.NoError(t, err)
	testNetNS, err := netns.New()
	assert.NoError(t, err)
	defer func() { assert.NoError(t, testNetNS.Close()) }()
	defer func() { assert.NoError(t, netns.Set(oldNetNS)) }()
	test()
}

func TestDevicesController(t *testing.T) {
	testutils.PrivilegedTest(t)
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	withFreshNetNS(t, func() {

		var (
			db           statedb.DB
			devicesTable statedb.Table[*tables.Device]
		)
		h := hive.New(
			statedb.Cell,
			tables.Cell,
			DevicesControllerCell,
			cell.Invoke(func(db_ statedb.DB, devicesTable_ statedb.Table[*tables.Device]) {
				db = db_
				devicesTable = devicesTable_
			}))

		err := h.Start(context.TODO())
		assert.NoError(t, err)

		devices := devicesTable.Reader(db.ReadTxn())

		it, err := devices.Get(statedb.All)
		assert.NoError(t, err)
		devs := statedb.Collect[*tables.Device](it)
		assert.Len(t, devs, 0)

		assert.NoError(t, createDummy("dummy0", "192.168.0.1/24", false))
		<-it.Invalidated()

		devices = devicesTable.Reader(db.ReadTxn())
		it, err = devices.Get(statedb.All)
		assert.NoError(t, err)
		devs = statedb.Collect[*tables.Device](it)
		assert.Len(t, devs, 1)
		// FIXME other asserts

		err = h.Stop(context.TODO())
		assert.NoError(t, err)
	})

}

/*



		// 1. No devices, nothing to detect.
		devices, err := dm.Detect(false)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{})

		// 2. Node IP not set, can still detect. Direct routing device shouldn't be detected.
		option.Config.EnableNodePort = true
		c.Assert(createDummy("dummy0", "192.168.0.1/24", false), IsNil)
		node.SetIPv4(nil)

		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"dummy0"})
		c.Assert(option.Config.GetDevices(), checker.DeepEquals, devices)
		c.Assert(option.Config.DirectRoutingDevice, Equals, "")

		// 3. Manually specified devices, no detection is performed
		option.Config.EnableNodePort = true
		node.SetIPv4(net.ParseIP("192.168.0.1"))
		c.Assert(createDummy("dummy1", "192.168.1.1/24", false), IsNil)
		option.Config.SetDevices([]string{"dummy0"})

		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"dummy0"})
		c.Assert(option.Config.GetDevices(), checker.DeepEquals, devices)
		c.Assert(option.Config.DirectRoutingDevice, Equals, "")
		option.Config.SetDevices([]string{})

		// 4. Direct routing mode, should find all devices and set direct
		// routing device to the one with k8s node ip.
		c.Assert(createDummy("dummy2", "192.168.2.1/24", false), IsNil)
		c.Assert(createDummy("dummy3", "192.168.3.1/24", false), IsNil)
		c.Assert(delRoutes("dummy3"), IsNil) // Delete routes so it won't be detected
		node.SetIPv4(net.ParseIP("192.168.1.1"))
		option.Config.EnableIPv4 = true
		option.Config.EnableIPv6 = false
		option.Config.Tunnel = option.TunnelDisabled
		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"dummy0", "dummy1", "dummy2"})
		c.Assert(option.Config.GetDevices(), checker.DeepEquals, devices)
		c.Assert(option.Config.DirectRoutingDevice, Equals, "dummy1")
		option.Config.DirectRoutingDevice = ""
		option.Config.SetDevices([]string{})

		// 5. Use IPv6 node IP and enable IPv6NDP and check that multicast device is detected.
		// Use an excluded device name to verify that device with NodeIP is still picked.
		option.Config.EnableIPv6 = true
		option.Config.EnableIPv6NDP = true
		c.Assert(createDummy("cilium_foo", "2001:db8::face/64", true), IsNil)
		node.SetIPv4(nil)
		node.SetIPv6(net.ParseIP("2001:db8::face"))
		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"cilium_foo", "dummy0", "dummy1", "dummy2"})
		c.Assert(option.Config.GetDevices(), checker.DeepEquals, devices)
		c.Assert(option.Config.DirectRoutingDevice, checker.Equals, "cilium_foo")
		c.Assert(option.Config.IPv6MCastDevice, checker.DeepEquals, "cilium_foo")
		option.Config.DirectRoutingDevice = ""
		option.Config.SetDevices([]string{})

		// 6. Only consider veth devices if they have a default route.
		c.Assert(createVeth("veth0", "192.168.4.1/24", false), IsNil)
		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"cilium_foo", "dummy0", "dummy1", "dummy2"})
		c.Assert(option.Config.GetDevices(), checker.DeepEquals, devices)
		option.Config.SetDevices([]string{})

		c.Assert(addRoute(addRouteParams{iface: "veth0", gw: "192.168.4.254", table: unix.RT_TABLE_MAIN}), IsNil)
		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"cilium_foo", "dummy0", "dummy1", "dummy2", "veth0"})
		c.Assert(option.Config.GetDevices(), checker.DeepEquals, devices)
		option.Config.SetDevices([]string{})

		// 7. Detect devices that only have routes in non-main tables
		c.Assert(addRoute(addRouteParams{iface: "dummy3", dst: "192.168.3.1/24", scope: unix.RT_SCOPE_LINK, table: 11}), IsNil)
		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"cilium_foo", "dummy0", "dummy1", "dummy2", "dummy3", "veth0"})
		c.Assert(option.Config.GetDevices(), checker.DeepEquals, devices)
		option.Config.SetDevices([]string{})

		// 8. Skip bridge devices, and devices added to the bridge
		c.Assert(createBridge("br0", "192.168.5.1/24", false), IsNil)
		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"cilium_foo", "dummy0", "dummy1", "dummy2", "dummy3", "veth0"})
		c.Assert(option.Config.GetDevices(), checker.DeepEquals, devices)
		option.Config.SetDevices([]string{})

		c.Assert(setMaster("dummy3", "br0"), IsNil)
		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"cilium_foo", "dummy0", "dummy1", "dummy2", "veth0"})
		c.Assert(option.Config.GetDevices(), checker.DeepEquals, devices)
		option.Config.SetDevices([]string{})

		// 9. Don't skip bond devices, but do skip bond slaves.
		c.Assert(createBond("bond0", "192.168.6.1/24", false), IsNil)
		c.Assert(setBondMaster("dummy2", "bond0"), IsNil)
		devices, err = dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"bond0", "cilium_foo", "dummy0", "dummy1", "veth0"})
		option.Config.SetDevices([]string{})
	})
}
*/

/*
func (s *DevicesSuite) TestListenForNewDevices(c *C) {
	s.withFreshNetNS(c, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		timeout := time.After(time.Second)

		option.Config.SetDevices([]string{})
		dm, err := NewDeviceManager()
		c.Assert(err, IsNil)

		devicesChan, err := dm.Listen(ctx)
		c.Assert(err, IsNil)

		// Create the IPv4 & IPv6 devices that should be detected.
		c.Assert(createDummy("dummy0", "192.168.1.2/24", false), IsNil)
		c.Assert(createDummy("dummy1", "2001:db8::face/64", true), IsNil)

		// Create another device without an IP address or routes. This should be ignored.
		c.Assert(createDummy("dummy2", "", false), IsNil)

		// Create a veth device which should be detected. veth devices are used in test
		// setups.
		c.Assert(createVeth("veth0", "192.168.2.2/24", false), IsNil)
		c.Assert(addRoute(addRouteParams{iface: "veth0", gw: "192.168.2.254", table: unix.RT_TABLE_MAIN}), IsNil)

		// Create few devices with excluded prefixes
		c.Assert(createDummy("lxc123", "", false), IsNil)
		c.Assert(createDummy("cilium_foo", "", false), IsNil)

		// Wait for the devices to be updated. Depending on how quickly the devices are created
		// this may span multiple callbacks.
		passed := false
		for !passed {
			select {
			case <-timeout:
				c.Fatal("Test timed out")
			case devices := <-devicesChan:
				passed, _ = checker.DeepEqual(devices, []string{"dummy0", "dummy1", "veth0"})
			}
		}

		// Test that deletion of devices is detected.
		link, err := netlink.LinkByName("dummy0")
		c.Assert(err, IsNil)
		err = netlink.LinkDel(link)
		c.Assert(err, IsNil)

		for !passed {
			select {
			case <-timeout:
				c.Fatal("Test timed out")
			case devices := <-devicesChan:
				passed, _ = checker.DeepEqual(devices, []string{"dummy1"})
			}
		}
	})
}

func (s *DevicesSuite) TestListenForNewDevicesFiltered(c *C) {
	s.withFreshNetNS(c, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		timeout := time.After(time.Second)

		option.Config.SetDevices([]string{"dummy+"})
		dm, err := NewDeviceManager()
		c.Assert(err, IsNil)

		devicesChan, err := dm.Listen(ctx)
		c.Assert(err, IsNil)

		// Create the IPv4 & IPv6 devices that should be detected.
		c.Assert(createDummy("dummy0", "192.168.1.2/24", false), IsNil)
		c.Assert(createDummy("dummy1", "2001:db8::face/64", true), IsNil)

		// Create a device with non-matching name.
		c.Assert(createDummy("other0", "192.168.2.2/24", false), IsNil)

		// Wait for the devices to be updated. Depending on how quickly the devices are created
		// this may span multiple callbacks.
		passed := false
		for !passed {
			select {
			case <-timeout:
				c.Fatal("Test timed out")
			case devices := <-devicesChan:
				passed, _ = checker.DeepEqual(devices, []string{"dummy0", "dummy1"})
			}
		}
	})
}

func (s *DevicesSuite) TestListenAfterDelete(c *C) {
	s.withFreshNetNS(c, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		timeout := time.After(time.Second)

		option.Config.SetDevices([]string{"dummy+"})
		dm, err := NewDeviceManager()
		c.Assert(err, IsNil)

		c.Assert(createDummy("dummy0", "192.168.1.2/24", false), IsNil)
		c.Assert(createDummy("dummy1", "2001:db8::face/64", true), IsNil)

		// Detect the devices
		devices, err := dm.Detect(true)
		c.Assert(err, IsNil)
		c.Assert(devices, checker.DeepEquals, []string{"dummy0", "dummy1"})

		// Delete one of the devices before listening
		link, err := netlink.LinkByName("dummy1")
		c.Assert(err, IsNil)
		err = netlink.LinkDel(link)
		c.Assert(err, IsNil)

		// Now start listening to device changes. We expect the dummy1 to
		// be deleted.
		devicesChan, err := dm.Listen(ctx)
		c.Assert(err, IsNil)

		passed := false
		for !passed {
			select {
			case <-timeout:
				c.Fatal("Test timed out")
			case devices := <-devicesChan:
				passed, _ = checker.DeepEqual(devices, []string{"dummy0"})
			}
		}
	})
}

func (s *DevicesSuite) withFreshNetNS(c *C, test func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	testNetNS, err := netns.New() // creates netns, and sets it to current
	c.Assert(err, IsNil)
	defer func() { c.Assert(testNetNS.Close(), IsNil) }()
	defer func() { c.Assert(netns.Set(s.currentNetNS), IsNil) }()

	test()
}

func createLink(linkTemplate netlink.Link, iface, ipAddr string, flagMulticast bool) error {
	var flags net.Flags
	if flagMulticast {
		flags = net.FlagMulticast
	}
	*linkTemplate.Attrs() = netlink.LinkAttrs{
		Name:  iface,
		Flags: flags,
	}

	if err := netlink.LinkAdd(linkTemplate); err != nil {
		return err
	}

	if ipAddr != "" {
		if err := addAddr(iface, ipAddr); err != nil {
			return err
		}
	}

	link, err := netlink.LinkByName(iface)
	if err != nil {
		return err
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return err
	}

	return nil
}

func createDummy(iface, ipAddr string, flagMulticast bool) error {
	return createLink(&netlink.Dummy{}, iface, ipAddr, flagMulticast)
}

func createVeth(iface, ipAddr string, flagMulticast bool) error {
	return createLink(&netlink.Veth{PeerName: iface + "_"}, iface, ipAddr, flagMulticast)
}

func createBridge(iface, ipAddr string, flagMulticast bool) error {
	return createLink(&netlink.Bridge{}, iface, ipAddr, flagMulticast)
}

func createBond(iface, ipAddr string, flagMulticast bool) error {
	bond := netlink.NewLinkBond(netlink.LinkAttrs{})
	bond.Mode = netlink.BOND_MODE_BALANCE_RR
	return createLink(bond, iface, ipAddr, flagMulticast)
}

func setMaster(iface string, master string) error {
	masterLink, err := netlink.LinkByName(master)
	if err != nil {
		return err
	}
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return err
	}
	return netlink.LinkSetMaster(link, masterLink)
}

func setBondMaster(iface string, master string) error {
	masterLink, err := netlink.LinkByName(master)
	if err != nil {
		return err
	}
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return err
	}

	netlink.LinkSetDown(link)
	return netlink.LinkSetBondSlave(link, masterLink.(*netlink.Bond))
}

func addAddr(iface string, cidr string) error {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	ipnet.IP = ip

	link, err := netlink.LinkByName(iface)
	if err != nil {
		return err
	}

	if err := netlink.AddrAdd(link, &netlink.Addr{IPNet: ipnet}); err != nil {
		return err
	}
	return nil
}

type addRouteParams struct {
	iface string
	gw    string
	src   string
	dst   string
	table int
	scope netlink.Scope
}

func addRoute(p addRouteParams) error {
	link, err := netlink.LinkByName(p.iface)
	if err != nil {
		return err
	}

	var dst *net.IPNet
	if p.dst != "" {
		_, dst, err = net.ParseCIDR(p.dst)
		if err != nil {
			return err
		}
	}

	var src net.IP
	if p.src != "" {
		src = net.ParseIP(p.src)
	}

	if p.table == 0 {
		p.table = unix.RT_TABLE_MAIN
	}

	route := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       dst,
		Src:       src,
		Gw:        net.ParseIP(p.gw),
		Table:     p.table,
		Scope:     p.scope,
	}
	if err := netlink.RouteAdd(route); err != nil {
		return err
	}

	return nil
}

func delRoutes(iface string) error {
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return err
	}

	filter := netlink.Route{
		Table:     unix.RT_TABLE_UNSPEC,
		LinkIndex: link.Attrs().Index,
	}
	mask := netlink.RT_FILTER_TABLE | netlink.RT_FILTER_OIF

	routes, err := netlink.RouteListFiltered(netlink.FAMILY_ALL, &filter, mask)
	if err != nil {
		return err
	}

	for _, r := range routes {
		if r.Table == unix.RT_TABLE_LOCAL {
			continue
		}
		if err := netlink.RouteDel(&r); err != nil {
			return err
		}
	}

	return nil
}*/
