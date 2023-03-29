package statedb

import memdb "github.com/hashicorp/go-memdb"

type CommitHook interface {
	Restore(TableName, *memdb.Txn) error
	Commit(changes []memdb.Change) error
}
