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
	),

	cell.Provide(state.TableSchemas(dummyTableSchema)),

	cell.Invoke(feedDummies),
)

var (
	dummyTableName = "test-dummies"
)

func dummyTable() state.Table[*Dummy] {
	return state.NewTable[*Dummy](dummyTableName)
}

var dummyTableSchema = &memdb.TableSchema{
	Name: dummyTableName,
	Indexes: map[string]*memdb.IndexSchema{
		"id": state.IDIndexSchema,
	},
}

type Dummy struct {
	ID  structs.UUID
	Foo string
}

func (d *Dummy) DeepCopy() *Dummy {
	return &Dummy{ID: d.ID, Foo: d.Foo}
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
