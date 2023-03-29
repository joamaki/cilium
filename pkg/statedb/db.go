// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package statedb

import (
	"encoding/json"
	"fmt"
	"io"

	memdb "github.com/hashicorp/go-memdb"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/stream"
)

func New(p params) (DB, error) {
	dbSchema := &memdb.DBSchema{
		Tables: make(map[string]*memdb.TableSchema),
	}
	hooks := map[TableName][]CommitHook{}
	for _, tableSchema := range p.Schemas {
		if _, ok := dbSchema.Tables[tableSchema.Name]; ok {
			panic(fmt.Sprintf("Table %q already registered", tableSchema.Name))
		}
		dbSchema.Tables[tableSchema.Name] = tableSchema.TableSchema
		hooks[TableName(tableSchema.Name)] = tableSchema.Hooks
	}
	memdb, err := memdb.NewMemDB(dbSchema)
	if err != nil {
		return nil, err
	}
	db := &stateDB{
		memDB:    memdb,
		revision: 0,
		hooks:    hooks,
	}
	db.Observable, db.emit, _ = stream.Multicast[Event]()

	p.Lifecycle.Append(db)

	return db, nil
}

// stateDB implements StateDB using go-memdb.
type stateDB struct {
	stream.Observable[Event]
	emit func(Event)

	memDB    *memdb.MemDB
	revision uint64 // Commit revision, protected by the write tx lock.
	hooks    map[TableName][]CommitHook
}

// Start implements hive.HookInterface
func (db *stateDB) Start(hive.HookContext) (err error) {
	// Restore state from hooks.
	txn := db.memDB.Txn(true)
loop:
	for table, hooks := range db.hooks {
		for _, hook := range hooks {
			err = hook.Restore(table, txn)
			if err != nil {
				break loop
			}
		}
	}
	if err != nil {
		txn.Abort()
	} else {
		txn.Commit()
	}
	return
}

// Stop implements hive.HookInterface
func (*stateDB) Stop(hive.HookContext) error {
	return nil
}

var _ DB = &stateDB{}
var _ hive.HookInterface = &stateDB{}

// WriteJSON marshals out the whole database as JSON into the given writer.
func (db *stateDB) WriteJSON(w io.Writer) error {
	tx := db.memDB.Txn(false)
	if _, err := w.Write([]byte("{\n")); err != nil {
		return err
	}
	for table := range db.memDB.DBSchema().Tables {
		iter, err := tx.Get(table, "id")
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte("\"" + table + "\": [\n")); err != nil {
			return err
		}
		obj := iter.Next()
		for obj != nil {
			bs, err := json.Marshal(obj)
			if err != nil {
				return err
			}
			if _, err := w.Write(bs); err != nil {
				return err
			}
			obj = iter.Next()
			if obj != nil {
				if _, err := w.Write([]byte(",")); err != nil {
					return err
				}
			}
		}
		if _, err := w.Write([]byte("]}\n")); err != nil {
			return err
		}
	}
	return nil
}

// WriteTxn constructs a new WriteTransaction
func (db *stateDB) WriteTxn() WriteTransaction {
	txn := db.memDB.Txn(true)
	txn.TrackChanges()
	return &transaction{
		db:  db,
		txn: txn,
		// Assign a revision to the transaction. Protected by
		// the memDB writer lock that we acquired with Txn(true).
		revision: db.revision + 1,
	}
}

// ReadTxn constructs a new ReadTransaction.
func (db *stateDB) ReadTxn() ReadTransaction {
	return &transaction{db: nil, txn: db.memDB.Txn(false)}
}

// transaction implements ReadTransaction and WriteTransaction using go-memdb.
type transaction struct {
	db       *stateDB
	revision uint64
	txn      *memdb.Txn
}

func (t *transaction) getTxn() *memdb.Txn { return t.txn }
func (t *transaction) Revision() uint64   { return t.revision }
func (t *transaction) Abort()             { t.txn.Abort() }
func (t *transaction) Defer(fn func())    { t.txn.Defer(fn) }

func (t *transaction) Commit() error {
	changesPerTable := map[string][]memdb.Change{}
	for _, change := range t.txn.Changes() {
		changesPerTable[change.Table] = append(changesPerTable[change.Table], change)

		// Verify that a copy of the original object is being
		// inserted rather than mutated in-place.
		if change.Before == change.After {
			panic("statedb: The original object is being modified without being copied first!")
		}
	}

	// Call registered commit hooks for each table with the per-table changes.
	for table, changes := range changesPerTable {
		for _, hook := range t.db.hooks[TableName(table)] {
			if err := hook.Commit(changes); err != nil {
				return err
			}
		}
	}

	// If all the hooks succeeded, commit the transaction to the in-memory state
	// and release the write lock.
	t.db.revision = t.revision
	t.txn.Commit()

	// Notify that these tables have changed. We are not concerned
	// about the order in which these events are received by subscribers as
	// this is only meant to be used as a trigger mechanism.
	for table := range changesPerTable {
		t.db.emit(Event{Table: TableName(table)})
	}
	return nil
}
