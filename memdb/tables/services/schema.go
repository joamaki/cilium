package services

import (
	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/hashicorp/go-memdb"
)

var (
	servicesTableName   = "int-services"
	servicesTableSchema = &memdb.TableSchema{
		Name: servicesTableName,
		Indexes: map[string]*memdb.IndexSchema{
			"id": state.IDIndexSchema,
		},
	}

	backendsTableName   = "int-backends"
	backendsTableSchema = &memdb.TableSchema{
		Name: servicesTableName,
		Indexes: map[string]*memdb.IndexSchema{
			"id": state.IDIndexSchema,
		},
	}
)

var Cell = cell.Provide(
	state.TableSchemas(
		servicesTableSchema,
		backendsTableSchema,
	),
	tables,
)

func tables() (state.Table[*Service], state.Table[*Backend]) {
	return state.NewTable[*Service](servicesTableName),
		state.NewTable[*Backend](backendsTableName)
}
