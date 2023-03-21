package tables

import (
	"fmt"
	"net"
	"net/netip"
	"reflect"

	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/hashicorp/go-memdb"
	"github.com/vishvananda/netlink"
)

var Cell = cell.Module(
	"tables",
	"Table definitions for datapath state",

	cell.Provide(

		state.TableSchemas(
			routeTableSchema,
			customRouteTableSchema,

			deviceTableSchema,
		),

		func() state.Table[*Route] { return state.NewTable[*Route](routeTableName) },
		func() state.Table[*CustomRoute] { return state.NewTable[*CustomRoute](customRouteTableName) },
		func() state.Table[*Device] { return state.NewTable[*Device](deviceTableName) },
	),
)

//
// Common index schemas
//

var (
	revisionIndexSchema = &memdb.IndexSchema{
		Name:         "revision",
		AllowMissing: false,
		Unique:       false,
		Indexer:      &memdb.UintFieldIndex{Field: "Revision"},
	}

	ifIndexSchema = &memdb.IndexSchema{
		Name:         "ifindex",
		AllowMissing: false,
		Unique:       false,
		Indexer:      &memdb.IntFieldIndex{Field: "IfIndex"},
	}
)

type addrIndexer struct {
	Field string
}

func (a addrIndexer) FromObject(obj interface{}) (bool, []byte, error) {
	v := reflect.ValueOf(obj)
	v = reflect.Indirect(v) // Dereference the pointer if any
	fv := v.FieldByName(a.Field)
	if !fv.IsValid() {
		return false, nil,
			fmt.Errorf("field '%s' for %#v is invalid", a.Field, obj)
	}
	// FIXME.
	addr, ok := fv.Interface().(fmt.Stringer)
	if !ok {
		return false, nil, fmt.Errorf("field '%s' is not net.IP", a.Field)
	}
	return true, []byte(addr.String()), nil
}

func (a addrIndexer) FromArgs(args ...interface{}) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("must provide only a single argument")
	}
	addr, ok := args[0].(*net.IPNet)
	if !ok {
		return nil, fmt.Errorf("arg is of type %T, want *net.IPNet", args[0])
	}
	return []byte(addr.String()), nil
}

//
// Queries
//

func ByIfIndex(ifindex int) state.Query {
	return state.Query{Index: state.Index("ifindex"), Args: []any{ifindex}}
}

func ByPrefix(prefix *net.IPNet) state.Query {
	return state.Query{Index: state.Index("id"), Args: []any{prefix}}
}

func ByName(name string) state.Query {
	return state.Query{Index: state.Index("name"), Args: []any{name}}
}

//
// Routes table
//
// Populated with the current routes in the system.
//

var (
	routeTableName    = "dp-routes"
	routeTableIndexes = map[string]*memdb.IndexSchema{
		// Prefix is the primary key.
		// TODO: It's not unique enough.
		"id": {
			Name:         "id",
			AllowMissing: false,
			Unique:       true,
			Indexer:      addrIndexer{"Prefix"},
		},
		"revision": revisionIndexSchema,
		"ifindex":  ifIndexSchema,
	}

	routeTableSchema = &memdb.TableSchema{
		Name:    routeTableName,
		Indexes: routeTableIndexes,
	}

	customRouteTableName   = "dp-custom-routes"
	customRouteTableSchema = &memdb.TableSchema{
		Name:    customRouteTableName,
		Indexes: routeTableIndexes,
	}
)

// adapted from pkg/datapath/linux/route/route.go.
type Route struct {
	Revision uint64

	// FIXME use netip.Prefix and netip.Addr.
	Prefix   *net.IPNet
	Nexthop  net.IP
	Local    net.IP
	IfIndex  int
	MTU      int
	Priority int
	Proto    int
	Scope    netlink.Scope
	Table    int
	Type     int
}

func (r *Route) DeepCopyInto(to *Route) {
	*to = *r
}

// CustomRoute is a route created by Cilium that should be applied to the system's
// routing table.
type CustomRoute Route

func (r *CustomRoute) DeepCopyInto(to *CustomRoute) {
	*to = *r
}

//
// Devices table
//

var (
	deviceTableName   = "dp-devices"
	deviceTableSchema = &memdb.TableSchema{
		Name: deviceTableName,
		Indexes: map[string]*memdb.IndexSchema{
			// The interface index is the primary key.
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
)

type Device struct {
	Index     int
	Name      string
	Addresses []netip.Addr

	// TODO revisions
}

func (d *Device) DeepCopyInto(to *Device) {
	*to = *d
	to.Addresses = append([]netip.Addr(nil), d.Addresses...)
}
