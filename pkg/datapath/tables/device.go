package tables

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/hashicorp/go-memdb"
	"github.com/vishvananda/netlink"
	"golang.org/x/exp/slices"

	"github.com/cilium/cilium/pkg/statedb"
)

type Device struct {
	Index  int             // Interface index (primary key)
	Name   string          // Interface name (e.g. eth0)
	Viable bool            // If true this device can be used by Cilium
	Addrs  []DeviceAddress // Addresses assigned to the device
	Link   netlink.Link    // The underlying link device
}

func (d *Device) String() string {
	return fmt.Sprintf("Device{Index:%d, Name:%s, len(Addrs):%d}", d.Index, d.Name, len(d.Addrs))
}

func (d *Device) DeepCopy() *Device {
	copy := *d
	copy.Addrs = slices.Clone(d.Addrs)
	return &copy
}

type DeviceAddress struct {
	Addr  netip.Addr
	Scope int // Routing table scope
	Flags int // Address flags
}

func (d *DeviceAddress) AsIP() net.IP {
	return d.Addr.AsSlice()
}

var deviceTableSchema = &memdb.TableSchema{
	Name: "devices",
	Indexes: map[string]*memdb.IndexSchema{
		"id": {
			Name:         "id",
			AllowMissing: false,
			Unique:       true,
			Indexer:      &memdb.IntFieldIndex{Field: "Index"},
		},
		"name": {
			Name:         "name",
			AllowMissing: false,
			Unique:       true,
			Indexer:      &memdb.StringFieldIndex{Field: "Name"},
		},
	},
}

func DeviceByIndex(index int) statedb.Query {
	return statedb.Query{
		Index: "id",
		Args:  []any{index},
	}
}

func DeviceByName(name string) statedb.Query {
	return statedb.Query{
		Index: "name",
		Args:  []any{name},
	}
}

// ViableDevices returns all current viable network devices.
// The invalidated channel is closed when devices have changed and
// should be requeried with a new transaction.
func ViableDevices(r statedb.TableReader[*Device]) (devs []*Device, invalidated <-chan struct{}) {
	iter, err := r.Get(statedb.All)
	if err != nil {
		// Devices table schema is malformed.
		panic(err)
	}
	for dev, ok := iter.Next(); ok; dev, ok = iter.Next() {
		if !dev.Viable {
			continue
		}
		devs = append(devs, dev)
	}
	return devs, iter.Invalidated()
}

func DeviceNames(devs []*Device) (names []string) {
	names = make([]string, len(devs))
	for i := range devs {
		names[i] = devs[i].Name
	}
	return
}

type NodePortAddr struct {
	IPv4 net.IP
	IPv6 net.IP
}

func (dev *Device) NodePortAddr() NodePortAddr {
	// FIXME compare the logic to node.firstGlobalAddr and make sure
	// we pick the same addresses. The Device.Addrs is sorted so that
	// it has the primary addresses with smallest scope on top so it
	// should be enough to just pick the top most address.

	// FIXME support option.Config.LBDevInheritIPAddr
	//
	// FIXME support multiple node port addresses per device.

	var a NodePortAddr
	for _, addr := range dev.Addrs {
		if a.IPv4 == nil && addr.Addr.Is4() {
			a.IPv4 = addr.AsIP()
		}
		if a.IPv6 == nil && addr.Addr.Is6() {
			a.IPv6 = addr.AsIP()
		}
	}
	return a
}
