package tables

import (
	"net/netip"

	"github.com/hashicorp/go-memdb"
	"golang.org/x/exp/slices"

	"github.com/cilium/cilium/pkg/statedb"
)

type Device struct {
	Index  int // Interface index (primary key)
	Name   string
	Addrs  []DeviceAddress
	Viable bool // If true, this device can be used by Cilium
}

type DeviceAddress struct {
	Addr  netip.Addr
	Scope int // Routing table scope
}

func (d *Device) DeepCopy() *Device {
	copy := *d
	copy.Addrs = slices.Clone(d.Addrs)
	return &copy
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
	},
}

func ByDeviceIndex(index int) statedb.Query {
	return statedb.Query{
		Index: "id",
		Args:  []any{index},
	}
}

func ByDeviceName(name string) statedb.Query {
	return statedb.Query{
		Index: "name",
		Args:  []any{name},
	}
}

// GetDevices returns all current viable network devices.
// The invalidated channel is closed when devices have changed and
// should be requeried.
func GetDevices(r statedb.TableReader[*Device]) (devs []*Device, invalidated <-chan struct{}) {
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
