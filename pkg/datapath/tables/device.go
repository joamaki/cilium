package tables

import (
	"net/netip"

	"github.com/hashicorp/go-memdb"
	"golang.org/x/exp/slices"

	"github.com/cilium/cilium/pkg/statedb"
)

type Device struct {
	Index int // Interface index (primary key)
	Name  string
	IPs   []netip.Addr // IP addresses
}

func (d *Device) DeepCopy() *Device {
	return &Device{
		Name: d.Name,
		IPs:  slices.Clone(d.IPs),
	}
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

func ByIndex(index int) statedb.Query {
	return statedb.Query{
		Index: "id",
		Args:  []any{index},
	}
}
