// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"

	"github.com/cilium/cilium/pkg/datapath/linux/probes"
	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/ip"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/mac"
	"github.com/cilium/cilium/pkg/statedb"
)

// DevicesControllerCell registers a controller that subscribes to network devices
// and routes via netlink and populates the devices and routes devices.
var DevicesControllerCell = cell.Module(
	"datapath-devices-controller",
	"Populates the device and route tables from netlink",

	cell.Invoke(registerDevicesController),

	// DeviceManager implements the old API for detecting and watching
	// devices.
	cell.Provide(newDeviceManager),
)

var (
	// batchingDuration is the amount of time to wait for more
	// addr/route/link updates before processing the batch.
	batchingDuration = 100 * time.Millisecond

	excludedDevicePrefixes = []string{
		defaults.HostDevice,
		"cilium_",
		"lo",
		"lxc",
		"cni",
		"docker",
	}

	// Route filter to look at all routing tables.
	routeFilter = netlink.Route{
		Table: unix.RT_TABLE_UNSPEC,
	}
	routeFilterMask = netlink.RT_FILTER_TABLE
)

type DevicesConfig struct {
	Devices []string
}

type devicesControllerParams struct {
	cell.In

	Log         logrus.FieldLogger
	DB          statedb.DB
	DeviceTable statedb.Table[*tables.Device]
	RouteTable  statedb.Table[*tables.Route]
	Config      DevicesConfig

	// NetNS is an optional network namespace handle. Used by tests to restrict the detection
	// to specific namespace.
	NetNS *netns.NsHandle `optional:"true"`
}

func registerDevicesController(lc hive.Lifecycle, p devicesControllerParams) {
	dc := &devicesController{
		devicesControllerParams: p,
		links:                   map[int]netlink.Link{},
		filter:                  deviceFilter(p.Config.Devices),
	}
	dc.ctx, dc.cancel = context.WithCancel(context.Background())
	lc.Append(dc)
}

type devicesController struct {
	devicesControllerParams
	filter         deviceFilter
	l3DevSupported bool
	handle         *netlink.Handle
	ctx            context.Context
	cancel         context.CancelFunc
	links          map[int]netlink.Link
}

func (dc *devicesController) Start(hive.HookContext) error {
	dc.l3DevSupported = probes.HaveProgramHelper(ebpf.SchedCLS, asm.FnSkbChangeHead) == nil

	// Construct a handle that uses the configured network namespace.
	netns := netns.None()
	if dc.NetNS != nil {
		netns = *dc.NetNS
	}
	var err error
	dc.handle, err = netlink.NewHandleAt(netns)
	if err != nil {
		return fmt.Errorf("NewHandleAt failed: %w", err)
	}

	// Subscribe to address updates to find out about link address changes
	// to update the device's addresses.
	addrUpdates := make(chan netlink.AddrUpdate, 16)
	opts := netlink.AddrSubscribeOptions{
		Namespace:    dc.NetNS,
		ListExisting: false,
	}
	if err := netlink.AddrSubscribeWithOptions(addrUpdates, dc.ctx.Done(), opts); err != nil {
		return fmt.Errorf("AddrSubscribeWithOptions failed: %w", err)
	}

	// Subscribe to route updates to find out if veth devices get a default
	// gateway.
	routeUpdates := make(chan netlink.RouteUpdate, 16)
	err = netlink.RouteSubscribeWithOptions(routeUpdates, dc.ctx.Done(),
		netlink.RouteSubscribeOptions{
			ListExisting: false,
			Namespace:    dc.NetNS,
		})
	if err != nil {
		return fmt.Errorf("RouteSubscribeWithOptions failed: %w", err)
	}

	// Subscribe to link updates to find out if a device is removed or
	// changes state to become unviable.
	linkUpdates := make(chan netlink.LinkUpdate, 16)
	err = netlink.LinkSubscribeWithOptions(linkUpdates, dc.ctx.Done(),
		netlink.LinkSubscribeOptions{
			ListExisting: false,
			Namespace:    dc.NetNS,
		})
	if err != nil {
		return fmt.Errorf("LinkSubscribeWithOptions failed: %w", err)
	}

	// Initialize the tables with the current tables.
	if err := dc.initialize(); err != nil {
		return err
	}

	// Start processing further updates in the background.
	go dc.loop(addrUpdates, routeUpdates, linkUpdates)

	return nil
}

func (dc *devicesController) Stop(hive.HookContext) error {
	dc.cancel()
	dc.handle.Close()

	// Unfortunately vishvananda/netlink is buggy and does not return from Recvfrom even
	// though the stop channel given to AddrSubscribeWithOptions or RouteSubscribeWithOptions
	// is closed. This is fixed by https://github.com/vishvananda/netlink/pull/793, which
	// isn't yet merged.
	// Due to this, we're currently not waiting here for loop() to exit and thus leaving around
	// couple goroutines until some address or route change arrive.
	return nil
}

func (dc *devicesController) initialize() error {
	// Do initial listing for each address, routes and links. We cannot use
	// 'ListExiting' option as it does not provide a mechanism to know when
	// the listing is done. Netlink does send a NLMSG_DONE, but this is not
	// handled by the library.
	batch := map[int][]any{}
	links, err := dc.handle.LinkList()
	if err != nil {
		return fmt.Errorf("LinkList failed: %w", err)
	}
	for _, link := range links {
		batch[link.Attrs().Index] = append(batch[link.Attrs().Index], netlink.LinkUpdate{
			Header: unix.NlMsghdr{Type: unix.RTM_NEWLINK},
			Link:   link,
		})
	}
	addrs, err := dc.handle.AddrList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("AddrList failed: %w", err)
	}
	for _, addr := range addrs {
		var ipnet net.IPNet
		if addr.IPNet != nil {
			ipnet = *addr.IPNet
		}
		batch[addr.LinkIndex] = append(batch[addr.LinkIndex], netlink.AddrUpdate{
			LinkAddress: ipnet,
			LinkIndex:   addr.LinkIndex,
			Flags:       addr.Flags,
			Scope:       addr.Scope,
			PreferedLft: addr.PreferedLft,
			ValidLft:    addr.ValidLft,
			NewAddr:     true,
		})
	}
	routes, err := dc.handle.RouteListFiltered(netlink.FAMILY_ALL, &routeFilter, routeFilterMask)
	if err != nil {
		return fmt.Errorf("RouteList failed: %w", err)
	}
	for _, route := range routes {
		batch[route.LinkIndex] = append(batch[route.LinkIndex], netlink.RouteUpdate{
			Type:  unix.RTM_NEWROUTE,
			Route: route,
		})
	}

	if !dc.processBatch(batch) {
		// This can only happen if the statedb rejects the commit.
		return errors.New("internal error: initial processing of devices failed")
	}

	{
		iter, _ := tables.ViableDevices(dc.DeviceTable.Reader(dc.DB.ReadTxn()))
		names := tables.DeviceNames(iter)
		dc.Log.WithField(logfields.Devices, names).Info("Detected initial devices")
	}

	return nil
}

func (dc *devicesController) loop(
	addrUpdates chan netlink.AddrUpdate,
	routeUpdates chan netlink.RouteUpdate,
	linkUpdates chan netlink.LinkUpdate,
) {
	timer := time.NewTimer(batchingDuration)
	if !timer.Stop() {
		<-timer.C
	}
	timerStopped := true

	batch := map[int][]any{}
	appendUpdate := func(index int, u any) {
		batch[index] = append(batch[index], u)
		if timerStopped {
			timer.Reset(batchingDuration)
			timerStopped = false
		}
	}

	for {
		select {
		case <-dc.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			for range addrUpdates {
			}
			for range routeUpdates {
			}
			for range linkUpdates {
			}
			return

		case u := <-addrUpdates:
			appendUpdate(u.LinkIndex, u)

		case r := <-routeUpdates:
			appendUpdate(r.LinkIndex, r)

		case l := <-linkUpdates:
			appendUpdate(int(l.Index), l)

		case <-timer.C:
			timerStopped = true
			if dc.processBatch(batch) {
				batch = map[int][]any{}
			}
		}
	}
}

func deviceAddressFromAddrUpdate(upd netlink.AddrUpdate) tables.DeviceAddress {
	return tables.DeviceAddress{
		Addr:  ip.MustAddrFromIP(upd.LinkAddress.IP),
		Flags: upd.Flags,
		Scope: upd.Scope,
	}
}

// processBatch processes a batch of link, route and address updates. If it successfully
// commits it to the state it returns true, if the batch is incomplete (e.g. route updates
// for non-existing link), it returns false and the batch should be expanded with newer
// updates and tried again.
func (dc *devicesController) processBatch(batch map[int][]any) (ok bool) {
	txn := dc.DB.WriteTxn()
	devicesWriter := dc.DeviceTable.Writer(txn)
	routesWriter := dc.RouteTable.Writer(txn)

	// Process link updates first so we can access the link metadata when processing
	// addresses and routes.
	for index, updates := range batch {
		for _, u := range updates {
			if l, ok := u.(netlink.LinkUpdate); ok {
				if dc.shouldSkipDevice(l) {
					delete(batch, index)
					continue
				}

				if l.Header.Type == unix.RTM_DELLINK {
					delete(dc.links, index)
					delete(batch, index)
					devicesWriter.DeleteAll(tables.DeviceByIndex(index))
					routesWriter.DeleteAll(tables.RouteByLinkIndex(index))
					break
				} else {
					dc.links[index] = l.Link
				}
			}
		}
	}

	for index, updates := range batch {
		link := dc.links[index]
		if link == nil {
			// Update for a link we have not yet observed, abort and try again
			// later.
			dc.Log.Debugf("link %d not found, retrying later", index)
			txn.Abort()
			return false
		}

		name := link.Attrs().Name

		d, _ := devicesWriter.First(tables.DeviceByIndex(index))
		if d == nil {
			d = &tables.Device{
				Index: index,
				Name:  name,
				Link:  link,
			}
		} else {
			d = d.DeepCopy()
		}

		for _, u := range updates {
			switch u := u.(type) {
			case netlink.AddrUpdate:
				if u.Scope == unix.RT_SCOPE_LINK {
					// Ignore link local addresses.
					continue
				}
				addr := deviceAddressFromAddrUpdate(u)
				if u.NewAddr {
					d.Addrs = append(d.Addrs, addr)
				} else {
					i := slices.Index(d.Addrs, addr)
					if i >= 0 {
						d.Addrs = slices.Delete(d.Addrs, i, i+1)
					}
				}
			case netlink.RouteUpdate:
				r := tables.Route{
					Table:     u.Table,
					LinkName:  name,
					LinkIndex: index,
					Scope:     uint8(u.Scope),
					Dst:       u.Dst,
					Src:       u.Src,
					Gw:        u.Gw,
				}
				if u.Type == unix.RTM_NEWROUTE {
					if err := routesWriter.Insert(&r); err != nil {
						log.WithError(err).Error("Insert into routes table failed")
					}
				} else {
					routesWriter.Delete(&r)
				}
			}
		}
		// Sort the addresses so that secondary addresses are at bottom and then
		// sorted by scope.
		sort.SliceStable(d.Addrs, func(i, j int) bool {
			return (d.Addrs[i].Flags&unix.IFA_F_SECONDARY < d.Addrs[j].Flags&unix.IFA_F_SECONDARY) &&
				(d.Addrs[i].Scope < d.Addrs[j].Scope)
		})

		// Revalidate if the device is still valid.
		d.Viable = dc.isViableDevice(link, routesWriter)

		if err := devicesWriter.Insert(d); err != nil {
			log.WithError(err).Error("Insert into devices table failed")
		}
	}

	if err := txn.Commit(); err != nil {
		log.WithError(err).Error("Committing devices and routes failed")
		return false
	}

	return true
}

// shouldSkipDevice returns true if the device should never be considered.
// If a device can later be used (e.g. it comes up or its routes change),
// then this method should return false and these type of checks should
// be made by isViableDevice.
func (dc *devicesController) shouldSkipDevice(link netlink.Link) bool {
	name := link.Attrs().Name

	// Never consider devices with any of the excluded devices.
	for _, p := range excludedDevicePrefixes {
		if strings.HasPrefix(name, p) {
			dc.Log.WithField(logfields.Device, name).
				Debugf("Skipping device as it has excluded prefix '%s'", p)
			return true
		}
	}

	// Ignore L3 devices if we cannot support them.
	if !dc.l3DevSupported && !mac.LinkHasMacAddr(link) {
		dc.Log.WithField(logfields.Device, name).
			Info("Skipping L3 device; >= 5.8 kernel is required.")
		return true
	}

	// If user specified devices or wildcards, then skip the device if it doesn't match.
	if !dc.filter.match(name) {
		dc.Log.WithField(logfields.Device, name).WithField("filter", dc.filter).Info("Skipping non-matching device")
		return true
	}

	switch link.Type() {
	case "bridge", "openvswitch":
		// Skip bridge devices as they're very unlikely to be used for K8s
		// purposes. In the rare cases where a user wants to load datapath
		// programs onto them they can override device detection with --devices.
		dc.Log.WithField(logfields.Device, name).Debug("Ignoring bridge-like device")
		return true

	}
	return false
}

// Exclude devices that have one or more of these flags set.
var excludedIfFlagsMask uint32 = unix.IFF_SLAVE | unix.IFF_LOOPBACK

// isViableDevice checks if the device is viable or not. We still maintain its state in
// case it later becomes viable.
func (dc *devicesController) isViableDevice(link netlink.Link, routes statedb.TableReader[*tables.Route]) bool {
	name := link.Attrs().Name
	log := dc.Log.WithField(logfields.Device, name)

	// Do not consider devices that are down.
	operState := link.Attrs().OperState
	if operState == netlink.OperDown {
		log.Debug("Ignoring device as its operational state is down")
		return false
	}

	// Skip devices that have an excluded interface flag set.
	if link.Attrs().RawFlags&excludedIfFlagsMask != 0 {
		log.Debugf("Ignoring device as it has excluded flag (%x)", link.Attrs().RawFlags)
		return false
	}

	// Ignore devices that are bonded or bridged.
	if link.Attrs().MasterIndex > 0 {
		log.Debug("Ignoring bonded or bridged device")
		return false
	}

	switch link.Type() {
	case "veth":
		// Skip veth devices that don't have a default route.
		// This is a workaround for kubernetes-in-docker. We want to avoid
		// veth devices in general as they may be leftovers from another CNI.
		if !tables.HasDefaultRoute(routes, link.Attrs().Index) {
			log.Debug("Ignoring veth device as it has no default route")
			return false
		}
	}

	if !hasGlobalRoute(link.Attrs().Index, routes) {
		log.Debugf("Ignoring device as it has no global unicast routes")
		return false
	}

	return true
}

func hasGlobalRoute(devIndex int, routes statedb.TableReader[*tables.Route]) bool {
	iter, _ := routes.Get(tables.RouteByLinkIndex(devIndex))
	for route, ok := iter.Next(); ok; route, ok = iter.Next() {
		if route.Dst != nil && route.Dst.IP.IsGlobalUnicast() {
			return true
		}
	}
	return false
}

type deviceFilter []string

func (lst deviceFilter) match(dev string) bool {
	if len(lst) == 0 {
		return true
	}
	for _, entry := range lst {
		if strings.HasSuffix(entry, "+") {
			prefix := strings.TrimRight(entry, "+")
			if strings.HasPrefix(dev, prefix) {
				return true
			}
		} else if dev == entry {
			return true
		}
	}
	return false
}
