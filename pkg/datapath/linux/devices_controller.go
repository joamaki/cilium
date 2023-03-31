package linux

import (
	"context"
	"fmt"
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
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/ip"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/mac"
	"github.com/cilium/cilium/pkg/statedb"
)

// DevicesControllerCell registers a controller that subscribes to network device
// IP addresses via netlink and populates the devices table.
//
// Requires [DevicesConfig] to be provided from outside.
// FIXME: Move relevant configuration flags from DaemonConfig to here.
var DevicesControllerCell = cell.Module(
	"datapath-devices-controller",
	"Populates the devices table from netlink",

	cell.Invoke(registerDevicesController),
)

// addrUpdateBatchingDuration is the amount of time to wait for more
// netlink.AddrUpdate messages before processing the batch.
var addrUpdateBatchingDuration = 100 * time.Millisecond

type DevicesConfig struct {
	Devices []string

	// NetNS is an optional network namespace handle. Used by tests to restrict the detection
	// to specific namespace.
	NetNS *netns.NsHandle
}

type devicesControllerParams struct {
	cell.In

	Log          logrus.FieldLogger
	DB           statedb.DB
	DevicesTable statedb.Table[*tables.Device]
	Config       DevicesConfig
}

func registerDevicesController(lc hive.Lifecycle, p devicesControllerParams) {
	dc := &devicesController{devicesControllerParams: p}
	dc.ctx, dc.cancel = context.WithCancel(context.Background())
	dc.filter = deviceFilter(p.Config.Devices)
	lc.Append(dc)
}

type devicesController struct {
	devicesControllerParams
	filter         deviceFilter
	l3DevSupported bool
	handle         *netlink.Handle
	ctx            context.Context
	cancel         context.CancelFunc
}

func (dc *devicesController) Start(hive.HookContext) error {
	dc.l3DevSupported = probes.HaveProgramHelper(ebpf.SchedCLS, asm.FnSkbChangeHead) == nil

	// Construct a handle that uses the configured network namespace.
	netns := netns.None()
	if dc.Config.NetNS != nil {
		netns = *dc.Config.NetNS
	}
	var err error
	dc.handle, err = netlink.NewHandleAt(netns)
	if err != nil {
		return fmt.Errorf("NewHandleAt failed: %w", err)
	}

	// Subscribe to address updates to find out about link address changes.
	updates := make(chan netlink.AddrUpdate, 16)
	opts := netlink.AddrSubscribeOptions{
		Namespace:    dc.Config.NetNS,
		ListExisting: false,
	}
	if err := netlink.AddrSubscribeWithOptions(updates, dc.ctx.Done(), opts); err != nil {
		return fmt.Errorf("AddrSubscribeWithOptions failed: %w", err)
	}

	// Subscribe to route updates to find out if veth devices get a default
	// gateway.
	routeUpdates := make(chan netlink.RouteUpdate, 16)
	err = netlink.RouteSubscribeWithOptions(routeUpdates, dc.ctx.Done(),
		netlink.RouteSubscribeOptions{
			ListExisting: true,
			Namespace:    dc.Config.NetNS,
		})
	if err != nil {
		return fmt.Errorf("RouteSubscribeWithOptions failed: %w", err)
	}

	// Subscribe to link updates to find out if a device gets bonded or bridged.
	linkUpdates := make(chan netlink.LinkUpdate, 16)
	err = netlink.LinkSubscribeWithOptions(linkUpdates, dc.ctx.Done(),
		netlink.LinkSubscribeOptions{
			ListExisting: true,
			Namespace:    dc.Config.NetNS,
		})
	if err != nil {
		return fmt.Errorf("LinkSubscribeWithOptions failed: %w", err)
	}

	// Dump all addresses to initially populate the table.
	addrs, err := dc.handle.AddrList(nil, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("AddrList failed: %w", err)
	}
	buf := map[int][]update{}
	for _, addr := range addrs {
		buf[addr.LinkIndex] = append(buf[addr.LinkIndex], updateFromAddr(addr))
	}
	dc.processBatch(buf, nil /* FIXME */)
	// TODO maybe better to wait for loop to signal that it's done with
	// committing the first batch of updates?

	// Start processing the updates in the background.
	go dc.loop(updates, routeUpdates, linkUpdates)

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

func (dc *devicesController) loop(updates chan netlink.AddrUpdate, routeUpdates chan netlink.RouteUpdate, linkUpdates chan netlink.LinkUpdate) {
	timer := time.NewTimer(addrUpdateBatchingDuration)
	if !timer.Stop() {
		<-timer.C
	}
	timerStopped := true

	buf := map[int][]update{}
	hasDefaultRoute := map[int]bool{}

	// FIXME drain the channels when done
	for {
		select {
		case u, ok := <-updates:
			if !ok {
				return
			}
			buf[u.LinkIndex] = append(buf[u.LinkIndex], updateFromAddrUpdate(u))
			if timerStopped {
				timer.Reset(addrUpdateBatchingDuration)
				timerStopped = false
			}

		case r, ok := <-routeUpdates:
			if !ok {
				return
			}
			if r.Dst == nil {
				if r.Type == unix.RTM_NEWROUTE {
					hasDefaultRoute[r.LinkIndex] = true
				} else {
					delete(hasDefaultRoute, r.LinkIndex)
				}

				// Mark this device for processing and start the timer.
				if _, ok := buf[r.LinkIndex]; !ok {
					buf[r.LinkIndex] = []update{}
					if timerStopped {
						timer.Reset(addrUpdateBatchingDuration)
						timerStopped = false
					}
				}
			}

		case l, ok := <-linkUpdates:
			if !ok {
				return
			}
			// Mark this device for processing and start the timer.
			if _, ok := buf[int(l.Index)]; !ok {
				buf[int(l.Index)] = []update{}
				if timerStopped {
					timer.Reset(addrUpdateBatchingDuration)
					timerStopped = false
				}
			}

		case <-timer.C:
			timerStopped = true
			if dc.processBatch(buf, hasDefaultRoute) {
				buf = map[int][]update{}
			}
		}
	}
}

type update struct {
	Addr    tables.DeviceAddress
	Deleted bool
}

func updateFromAddr(addr netlink.Addr) update {
	return update{
		Addr: tables.DeviceAddress{
			Addr:  ip.MustAddrFromIP(addr.IP),
			Scope: addr.Scope,
		},
		Deleted: false,
	}
}

func updateFromAddrUpdate(upd netlink.AddrUpdate) update {
	return update{
		Addr: tables.DeviceAddress{
			Addr:  ip.MustAddrFromIP(upd.LinkAddress.IP),
			Scope: upd.Scope,
		},
		Deleted: !upd.NewAddr,
	}
}

func (dc *devicesController) processBatch(updatesByLink map[int][]update, hasDefaultRoute map[int]bool) (ok bool) {
	// Gather the links first as we don't want to be doing syscalls within a write transaction.
	// If getting the link fails we assume it has been deleted.
	links := map[int]netlink.Link{}
	for index := range updatesByLink {
		links[index], _ = dc.handle.LinkByIndex(index)
	}

	txn := dc.DB.WriteTxn()
	w := dc.DevicesTable.Writer(txn)

	var err error
	for index, updates := range updatesByLink {
		link := links[index]
		if link == nil {
			w.Delete(&tables.Device{Index: index})
			continue
		}
		if dc.shouldSkipDevice(link) {
			continue
		}

		name := link.Attrs().Name

		d, _ := w.First(tables.ByDeviceIndex(index))
		if d == nil {
			d = &tables.Device{
				Index: index,
				Name:  name,
			}
		} else {
			d = d.DeepCopy()
		}

		d.Viable = dc.isViableDevice(link, hasDefaultRoute)

		for _, u := range updates {
			if u.Addr.Scope == unix.RT_SCOPE_LINK {
				// Ignore link local addresses.
				continue
			}

			if u.Deleted {
				i := slices.Index(d.Addrs, u.Addr)
				if i >= 0 {
					d.Addrs = slices.Delete(d.Addrs, i, i+1)
				}
			} else {
				d.Addrs = append(d.Addrs, u.Addr)
			}
		}
		// Sort the addresses by scope, with universe on top.
		// TODO secondary addresses?
		sort.SliceStable(d.Addrs, func(i, j int) bool {
			return d.Addrs[i].Scope < d.Addrs[j].Scope
		})

		err = w.Insert(d)
		if err != nil {
			log.WithError(err).Error("Insert into devices table failed")
			break
		}
	}
	if err != nil {
		txn.Abort()
		return false
	} else if err = txn.Commit(); err != nil {
		log.WithError(err).Error("Committing to devices table failed")
		return false
	}
	return true
}

// Exclude devices that have one or more of these flags set.
//var excludedIfFlagsMask uint32 = unix.IFF_SLAVE | unix.IFF_LOOPBACK

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
			Info("Ignoring L3 device; >= 5.8 kernel is required.")
		return true
	}

	// If user specified devices or wildcards, then skip the device if it doesn't match.
	if !dc.filter.match(name) {
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

// isViableDevice checks if the device is viable or not. We still maintain its state in
// case it later becomes viable.
func (dc *devicesController) isViableDevice(link netlink.Link, hasDefaultRoute map[int]bool) bool {
	// isViableDevice returns true if the given link is usable and Cilium should attach
	// programs to it.
	name := link.Attrs().Name

	// Do not consider devices that are down.
	operState := link.Attrs().OperState
	if operState == netlink.OperDown {
		dc.Log.WithField(logfields.Device, name).
			Debug("Skipping device as it's operational state is down")
		return false
	}

	// Skip devices that have an excluded interface flag set.
	if link.Attrs().RawFlags&excludedIfFlagsMask != 0 {
		dc.Log.WithField(logfields.Device, name).Debugf("Skipping device as it has excluded flag (%x)", link.Attrs().RawFlags)
		return false
	}

	switch link.Type() {
	case "veth":
		// Skip veth devices that don't have a default route.
		// This is a workaround for kubernetes-in-docker. We want to avoid
		// veth devices in general as they may be leftovers from another CNI.
		if !hasDefaultRoute[link.Attrs().Index] {
			dc.Log.WithField(logfields.Device, name).
				Debug("Ignoring veth device as it has no default route")
			return false
		}
	}

	// TODO. Any reason why we need to look at the type?
	if link.Attrs().MasterIndex > 0 {
		if master, err := dc.handle.LinkByIndex(link.Attrs().MasterIndex); err == nil {
			switch master.Type() {
			case "bridge", "openvswitch":
				dc.Log.WithField(logfields.Device, name).Debug("Ignoring device attached to bridge")
				return false

			case "bond", "team":
				dc.Log.WithField(logfields.Device, name).Debug("Ignoring bonded device")
				return false
			}

		}
	}

	return true
}
