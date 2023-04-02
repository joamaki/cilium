package tables

import (
	"fmt"
	"net"

	"github.com/hashicorp/go-memdb"
	"golang.org/x/sys/unix"

	"github.com/cilium/cilium/pkg/statedb"
)

type Route struct {
	Table     int
	LinkName  string
	LinkIndex int

	Scope uint8
	Dst   *net.IPNet
	Src   net.IP
	Gw    net.IP
}

func (r *Route) DeepCopy() *Route {
	r2 := *r
	if r2.Dst != nil {
		r2.Dst = &net.IPNet{
			IP:   r.Dst.IP,
			Mask: r.Dst.Mask,
		}
	}
	return &r2
}

func (r *Route) String() string {
	return fmt.Sprintf("Route{Dst: %s, Src: %s, Table: %d, LinkName: %s}",
		r.Dst, r.Src, r.Table, r.LinkName)
}

var routeTableSchema = &memdb.TableSchema{
	Name: "routes",
	Indexes: map[string]*memdb.IndexSchema{
		"id": {
			Name:         "id",
			AllowMissing: false,
			Unique:       true,
			Indexer: &memdb.CompoundIndex{
				Indexes: []memdb.Indexer{
					&memdb.IntFieldIndex{Field: "Table"},
					&memdb.IntFieldIndex{Field: "LinkIndex"},
					&IPNetFieldIndex{Field: "Dst"},
				},
			},
		},
		"LinkName": {
			Name:         "LinkName",
			AllowMissing: false,
			Unique:       false,
			Indexer:      &memdb.StringFieldIndex{Field: "LinkName"},
		},
		"LinkIndex": {
			Name:         "LinkIndex",
			AllowMissing: false,
			Unique:       false,
			Indexer:      &memdb.IntFieldIndex{Field: "LinkIndex"},
		}},
}

func ByRouteLinkName(name string) statedb.Query {
	return statedb.Query{
		Index: "LinkName",
		Args:  []any{name},
	}
}

func ByRouteLinkIndex(index int) statedb.Query {
	return statedb.Query{
		Index: "LinkIndex",
		Args:  []any{index},
	}
}

func HasDefaultRoute(reader statedb.TableReader[*Route], linkIndex int) bool {
	// Device has a default route when a route exists in the main table
	// with a zero destination.
	q := statedb.Query{
		Index: "id",
		Args: []any{
			unix.RT_TABLE_MAIN,
			linkIndex,
			nil,
		},
	}
	r, err := reader.First(q)
	if err != nil {
		panic(fmt.Sprintf("Internal error: Query %+v is malformed (%s)", q, err))
	}
	return r != nil
}
