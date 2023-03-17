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
			"id":                       state.IDIndexSchema,
			string(serviceSourceIndex): serviceSourceIndexSchema,
			string(state.NameIndex):    state.NameIndexSchema,
			"revision":                 serviceRevisionIndexSchema,
		},
	}

	serviceSourceIndex       = state.Index("source")
	serviceSourceIndexSchema = &memdb.IndexSchema{
		Name:         string(serviceSourceIndex),
		AllowMissing: false,
		Unique:       false,
		Indexer:      &memdb.StringFieldIndex{Field: "Source"},
	}
	serviceRevisionIndexSchema = &memdb.IndexSchema{
		Name:         "revision",
		AllowMissing: false,
		Unique:       false,
		Indexer:      &memdb.UintFieldIndex{Field: "Revision"},
	}

	backendsTableName   = "int-backends"
	backendsTableSchema = &memdb.TableSchema{
		Name: backendsTableName,
		Indexes: map[string]*memdb.IndexSchema{
			"id": state.IDIndexSchema,
		},
	}
)

func BySource(source ServiceSource) state.Query {
	return state.Query{Index: serviceSourceIndex, Args: []any{source}}
}

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
