package main

import (
	"context"
	"os"
	"time"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/reconciler"
)

var Hive = hive.New(
	statedb.Cell,
	reconciler.Cell,
	job.Cell,

	Example,
)

var Example = cell.Module(
	"example",
	"Reconciler example",

	cell.Config(Config{}),
	cell.Provide(NewMemoTable),
	cell.Invoke(statedb.RegisterTable[*Memo]),

	cell.Provide(NewTarget),
	cell.Provide(NewReconcilerConfig),
	cell.Invoke(reconciler.Register[*Memo]),

	// Setup and dump the Memo table periodically.
	cell.Invoke(fillSomeStuff),
	cell.Invoke(periodicDump),
)

func NewReconcilerConfig() reconciler.Config[*Memo] {
	return reconciler.Config[*Memo]{
		FullReconcilationInterval: 10 * time.Second,
		RetryBackoffMinDuration:   100 * time.Millisecond,
		RetryBackoffMaxDuration:   5 * time.Second,
		GetObjectStatus:           (*Memo).GetStatus,
		WithObjectStatus:          (*Memo).WithStatus,
	}
}

func main() {
	Hive.Run()
}

func fillSomeStuff(db *statedb.DB, memos statedb.RWTable[*Memo]) {
	txn := db.WriteTxn(memos)
	defer txn.Commit()

	memos.Insert(
		txn,
		&Memo{
			Name:    "hello",
			Content: "hello world\n",
			Status:  reconciler.StatusPending(),
		})

}

func periodicDump(lc hive.Lifecycle, r job.Registry, scope cell.Scope, db *statedb.DB) {
	g := r.NewGroup(scope)
	lc.Append(g)

	g.Add(job.Timer(
		"dump",
		func(ctx context.Context) error {
			return db.ReadTxn().WriteJSON(os.Stdout)
		},
		30*time.Second,
	))
}
