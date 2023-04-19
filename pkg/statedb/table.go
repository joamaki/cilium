// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package statedb

import (
	"sync/atomic"

	memdb "github.com/hashicorp/go-memdb"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
)

// NewTableCell constructs a new hive cell for a table. Provides Table[Obj] to the application
// and registers the table's schema with the database.
//
// Example usage:
//
//	var beeTableSchema = &memdb.TableSchema{...}
//	cell.Module(
//	  "bee-table",
//	  "Bees!",
//
//	  statedb.NewTableCell[*Bee](beeTableSchema), // Provides statedb.Table[*Bee] and register the schema.
//	  cell.Provide(New)
//	)
//	type Bee inteface {
//	  // some nicer accessors to Table[*Bee]
//	}
//	func New(bees state.Table[*Bee]) Bee { ... }
func NewTableCell[Obj ObjectConstraints[Obj]](schema *memdb.TableSchema) cell.Cell {
	return cell.Provide(
		newTable[Obj](schema),
		func() tableSchemaOut { return tableSchemaOut{Schema: schema} },
	)
}

// NewPrivateTableCell is like NewTableCell, but provides Table[Obj] privately, e.g. only
// to the module defining it.
func NewPrivateTableCell[Obj ObjectConstraints[Obj]](schema *memdb.TableSchema) cell.Cell {
	return cell.Group(
		cell.ProvidePrivate(newTable[Obj](schema)),
		cell.Provide(
			func() tableSchemaOut { return tableSchemaOut{Schema: schema} },
		),
	)
}

type tableSchemaOut struct {
	cell.Out

	Schema *memdb.TableSchema `group:"statedb-table-schemas"`
}

func newTable[Obj ObjectConstraints[Obj]](schema *memdb.TableSchema) func(lc hive.Lifecycle) Table[Obj] {
	return func(lc hive.Lifecycle) Table[Obj] {
		t := &table[Obj]{
			table:        schema.Name,
			initialized:  make(chan struct{}),
			initializers: map[string]chan struct{}{},
		}
		lc.Append(t)
		return t
	}
}

type table[Obj ObjectConstraints[Obj]] struct {
	started      atomic.Bool
	table        string
	initialized  chan struct{}
	initializers map[string]chan struct{}
}

// Start implements hive.HookInterface
func (t *table[Obj]) Start(hive.HookContext) error {
	t.started.Store(true)
	go func() {
		for _, ch := range t.initializers {
			<-ch
		}
		close(t.initialized)
	}()
	return nil
}

// Stop implements hive.HookInterface
func (t *table[Obj]) Stop(hive.HookContext) error {
	return nil
}

func (t *table[Obj]) Name() TableName {
	return TableName(t.table)
}

func (t *table[Obj]) Initialized() <-chan struct{} {
	if !t.started.Load() {
		panic("Initialized() called before Start()")
	}
	return t.initialized
}

func (t *table[Obj]) RegisterInitializer(name string) chan<- struct{} {
	if t.started.Load() {
		panic("RegisterInitializer called after Start()")
	}
	ch := make(chan struct{})
	t.initializers[name] = ch
	return ch
}

func (t *table[Obj]) Reader(tx ReadTransaction) TableReader[Obj] {
	return &tableTxn[Obj]{
		table: string(t.table),
		txn:   tx.getTxn(),
	}
}

func (t *table[Obj]) Writer(tx WriteTransaction) TableReaderWriter[Obj] {
	return &tableTxn[Obj]{
		table: string(t.table),
		txn:   tx.getTxn(),
	}
}

type tableTxn[Obj any] struct {
	table string
	txn   *memdb.Txn
}

func (t *tableTxn[Obj]) Delete(obj Obj) error {
	return t.txn.Delete(t.table, obj)
}

func (t *tableTxn[Obj]) DeleteAll(q Query) (int, error) {
	return t.txn.DeleteAll(t.table, string(q.Index), q.Args...)
}

func (t *tableTxn[Obj]) First(q Query) (obj Obj, err error) {
	var v any
	v, err = t.txn.First(t.table, string(q.Index), q.Args...)
	if err == nil && v != nil {
		obj = v.(Obj)
	}
	return
}

func (t *tableTxn[Obj]) Get(q Query) (WatchableIterator[Obj], error) {
	it, err := t.txn.Get(t.table, string(q.Index), q.Args...)
	if err != nil {
		return nil, err
	}
	return iterator[Obj]{it}, nil
}

func (t *tableTxn[Obj]) LowerBound(q Query) (Iterator[Obj], error) {
	it, err := t.txn.LowerBound(t.table, string(q.Index), q.Args...)
	if err != nil {
		return nil, err
	}
	return iterator[Obj]{it}, nil
}

func (t *tableTxn[Obj]) Insert(obj Obj) error {
	return t.txn.Insert(t.table, obj)
}

func (t *tableTxn[Obj]) Last(q Query) (obj Obj, err error) {
	var v any
	v, err = t.txn.Last(t.table, string(q.Index), q.Args...)
	if err == nil && v != nil {
		obj = v.(Obj)
	}
	return
}
