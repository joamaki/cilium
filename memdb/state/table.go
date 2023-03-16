package state

import (
	memdb "github.com/hashicorp/go-memdb"
)

type WriteTransaction interface {
	getTxn() *memdb.Txn

	Abort()
	Commit() error
	Defer(fn func())
}

type ReadTransaction interface {
	getTxn() *memdb.Txn
}

// ObjectConstraints specifies the constraints that an object
// must fulfill for it to be stored in a table.
type ObjectConstraints[Obj any] interface {
	DeepCopy() Obj
}

type Table[Obj ObjectConstraints[Obj]] interface {
	Name() string
	Read(tx ReadTransaction) TableReader[Obj]
	Modify(tx WriteTransaction) TableReaderWriter[Obj]
}

type TableReader[Obj any] interface {
	First(Query) (Obj, error)
	Last(Query) (Obj, error)
	Get(Query) (WatchableIterator[Obj], error)

	LowerBound(Query) (Iterator[Obj], error)
}

type TableReaderWriter[Obj any] interface {
	TableReader[Obj]

	Insert(obj Obj) error
	Delete(obj Obj) error
	DeleteAll(Query) (n int, err error)
}

type table[Obj any] struct {
	table string
}

func NewTable[Obj ObjectConstraints[Obj]](tableName string) Table[Obj] {
	return &table[Obj]{table: tableName}
}

func (t *table[Obj]) Name() string {
	return t.table
}

func (t *table[Obj]) Read(tx ReadTransaction) TableReader[Obj] {
	return &tableTxn[Obj]{table: t.table, tx: tx.getTxn()}
}

func (t *table[Obj]) Modify(tx WriteTransaction) TableReaderWriter[Obj] {
	return &tableTxn[Obj]{table: t.table, tx: tx.getTxn()}
}

type tableTxn[Obj any] struct {
	table string
	tx    *memdb.Txn
}

func (t *tableTxn[Obj]) Delete(obj Obj) error {
	return t.tx.Delete(t.table, obj)
}

func (t *tableTxn[Obj]) DeleteAll(q Query) (int, error) {
	return t.tx.DeleteAll(t.table, string(q.Index), q.Args...)
}

func (t *tableTxn[Obj]) First(q Query) (obj Obj, err error) {
	var v any
	v, err = t.tx.First(t.table, string(q.Index), q.Args...)
	if err == nil && v != nil {
		obj = v.(Obj)
	}
	// TODO not found error or zero value Obj is fine?
	return
}

func (t *tableTxn[Obj]) Get(q Query) (WatchableIterator[Obj], error) {
	it, err := t.tx.Get(t.table, string(q.Index), q.Args...)
	if err != nil {
		return nil, err
	}
	return iterator[Obj]{it}, nil
}

func (t *tableTxn[Obj]) LowerBound(q Query) (Iterator[Obj], error) {
	it, err := t.tx.LowerBound(t.table, string(q.Index), q.Args...)
	if err != nil {
		return nil, err
	}
	return iterator[Obj]{it}, nil
}

func (t *tableTxn[Obj]) Insert(obj Obj) error {
	return t.tx.Insert(t.table, obj)
}

func (t *tableTxn[Obj]) Last(q Query) (obj Obj, err error) {
	var v any
	v, err = t.tx.Last(t.table, string(q.Index), q.Args...)
	if err == nil && v != nil {
		obj = v.(Obj)
	}
	// TODO not found error or zero value Obj is fine?
	return
}
