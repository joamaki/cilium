package datasources

import (
	"time"

	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/memdb/state/structs"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/hashicorp/go-memdb"
)

// Simple test for datasource that also implements the table schema and provides
// the state.Table[*Dummy].

var dummyCell = cell.Module(
	"datasource-dummy",
	"Dummy data source",

	cell.Provide(
		dummyTable,
		dummyTableSchema,
	),

	cell.Invoke(feedDummies),
)

var (
	dummyTableName = "test-dummies"
)

func dummyTable() state.Table[*Dummy] {
	return state.NewTable[*Dummy](dummyTableName)
}

func dummyTableSchema() (out state.TableSchemaOut) {
	out.Schema = &memdb.TableSchema{
		Name: dummyTableName,
		Indexes: map[string]*memdb.IndexSchema{
			"id": state.IDIndexSchema,
		},
	}
	return
}

type Dummy struct {
	ID  structs.UUID
	Foo string
}

func feedDummies(lc hive.Lifecycle, s *state.State, dummyTable state.Table[*Dummy]) {
	onStart := func() {
		for {
			tx := s.Write()
			dummies := dummyTable.Modify(tx)
			err := dummies.Insert(&Dummy{ID: structs.NewUUID(), Foo: "foo"})
			if err != nil {
				panic(err)
			}
			tx.Commit()
			time.Sleep(time.Second)
		}
	}

	lc.Append(hive.Hook{OnStart: func(hive.HookContext) error { go onStart(); return nil }})

}
