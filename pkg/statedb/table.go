// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package statedb

import (
	memdb "github.com/hashicorp/go-memdb"

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
func NewTableCell[Obj ObjectConstraints[Obj]](schema *memdb.TableSchema, opts ...tableOption) cell.Cell {
	var o tableOptions
	for _, opt := range opts {
		opt(&o)
	}

	newTable := func() Table[Obj] { return &table[Obj]{table: schema.Name} }
	newSchema := func() tableSchemaOut { return tableSchemaOut{Schema: &TableSchema{schema, o.hooks}} }

	if o.private {
		return cell.Group(
			cell.ProvidePrivate(newTable),
			cell.Provide(newSchema),
		)
	} else {
		return cell.Provide(newTable, newSchema)
	}
}

type tableOptions struct {
	private bool
	hooks   []CommitHook
}

type tableOption func(*tableOptions)

var Private tableOption = func(o *tableOptions) { o.private = true }

func WithHook(hook CommitHook) tableOption {
	return func(o *tableOptions) {
		o.hooks = append(o.hooks, hook)
	}
}

type tableSchemaOut struct {
	cell.Out

	Schema *TableSchema `group:"statedb-table-schemas"`
}

type TableSchema struct {
	*memdb.TableSchema
	Hooks []CommitHook
}

type table[Obj ObjectConstraints[Obj]] struct {
	table string
}

func (t *table[Obj]) Name() TableName {
	return TableName(t.table)
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
