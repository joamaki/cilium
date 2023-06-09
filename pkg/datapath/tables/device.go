// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package tables

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/hashicorp/go-memdb"
	"golang.org/x/exp/slices"

	"github.com/cilium/cilium/pkg/statedb"
)

const (
	DeviceNameIndex statedb.Index = "Name"
)

var deviceTableSchema = &memdb.TableSchema{
	Name: "devices",
	Indexes: map[string]*memdb.IndexSchema{
		string(statedb.IDIndex): {
			Name:         string(statedb.IDIndex),
			AllowMissing: false,
			Unique:       true,
			Indexer:      &memdb.IntFieldIndex{Field: "Index"},
		},
		string(DeviceNameIndex): {
			Name:         string(DeviceNameIndex),
			AllowMissing: false,
			Unique:       true,
			Indexer:      &memdb.StringFieldIndex{Field: "Name"},
		},
	},
}

// Device is a local network device along with addresses associated with it.
type Device struct {
	net.Interface

	Viable bool            // If true this device can be used by Cilium
	Addrs  []DeviceAddress // Addresses assigned to the device
}

func (d *Device) String() string {
	return fmt.Sprintf("Device{Index:%d, Name:%s, len(Addrs):%d}", d.Index, d.Name, len(d.Addrs))
}

func (d *Device) DeepCopy() *Device {
	copy := *d
	copy.Addrs = slices.Clone(d.Addrs)
	return &copy
}

func (d *Device) HasIP(ip net.IP) bool {
	for _, addr := range d.Addrs {
		if addr.AsIP().Equal(ip) {
			return true
		}
	}
	return false
}

type NodePortAddr struct {
	IPv4 net.IP
	IPv6 net.IP
}

// NodePortAddr returns the NodePort frontend addresses chosen
// from this device.
func (dev *Device) NodePortAddr() NodePortAddr {
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

type DeviceAddress struct {
	Addr  netip.Addr
	Scope int // Routing table scope
	Flags int // Address flags
}

func (d *DeviceAddress) AsIP() net.IP {
	return d.Addr.AsSlice()
}

// DeviceByIndex constructs a query to find a device by its
// interface index.
func DeviceByIndex(index int) statedb.Query {
	return statedb.Query{Index: statedb.IDIndex, Args: []any{index}}
}

// DeviceByName constructs a query to find a device by its
// name.
func DeviceByName(name string) statedb.Query {
	return statedb.Query{Index: DeviceNameIndex, Args: []any{name}}
}

// ViableDevices returns all current viable network devices.
// The invalidated channel is closed when devices have changed and
// should be requeried with a new transaction.
func ViableDevices(r statedb.TableReader[*Device]) (devs []*Device, invalidated <-chan struct{}) {
	iter, err := r.Get(statedb.Query{Index: DeviceNameIndex})
	if err != nil {
		// table schema is malformed?
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

// DeviceNames extracts the device names from a slice of devices.
func DeviceNames(devs []*Device) (names []string) {
	names = make([]string, len(devs))
	for i := range devs {
		names[i] = devs[i].Name
	}
	return
}
