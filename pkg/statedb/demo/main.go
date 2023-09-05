package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

func main() {
	logging.SetLogLevel(logrus.ErrorLevel)
	Demo.Start(context.Background())
	Demo.Stop(context.Background())
}

var Demo = hive.New(
	statedb.Cell,

	demoTables,

	cell.Invoke(demo),
)

func demo(db *statedb.DB, baskets statedb.Table[*Basket], apples statedb.Table[*Apple]) {
	fmt.Println("--- Print baskets")

	// Create a ReadTxn
	// func (db *DB) ReadTxn() ReadTxn
	rtxn := db.ReadTxn()

	// Print the current baskets and take a watch channel to wait for baskets to change.
	watch := printBaskets(rtxn, baskets, apples)

	// -----------------------------------------------------------------------------

	fmt.Println("\n--- Create basket and apples")

	// Construct a write transaction against the baskets and apples. The transaction
	// object is not thread-safe as it builds up the state changes that are committed.
	// A write transaction locks the tables, so it's important to keep the write transaction
	// short and avoid any long operations.
	// func (db *DB) WriteTxn(table TableMeta, tables ...TableMeta) WriteTxn
	txn := db.WriteTxn(baskets, apples)

	// Add a basket with some apples.
	// Insert(WriteTxn, Obj) (oldObj Obj, hadOld bool, err error)
	red1, red2, green := newApple("red"), newApple("red"), newApple("green")
	apples.Insert(txn, red1)
	apples.Insert(txn, red2)
	apples.Insert(txn, green)
	basket := &Basket{Name: "small", Apples: []uuid.UUID{red1.ID, red2.ID, green.ID}}
	baskets.Insert(txn, basket)

	// We ignored the errors above for brevity. Insert can fail if the WriteTxn has
	// already been committed, or if the target table was not locked for writing.

	// Start tracking deletions of baskets.
	// DeleteTracker(txn WriteTxn, trackerName string) (*DeleteTracker[Obj], error)
	dt, err := baskets.DeleteTracker(txn, "demo")
	if err != nil {
		panic(err)
	}
	defer dt.Close()

	txn.Commit()

	fmt.Printf("Created basket %q\n", basket.Name)

	// -----------------------------------------------------------------------------

	fmt.Println("\n--- Print baskets again after insert")

	// The original read transaction is unaffected by the changes.
	printBaskets(rtxn, baskets, apples)

	// Wait for changes since the original read, create a new read transaction
	// and print again.
	<-watch
	rtxn = db.ReadTxn()
	watch = printBaskets(rtxn, baskets, apples)

	// Apples can be queried in different ways with the indexes we created.
	printApplesOfColor(rtxn, apples, "red")
	printApplesWithColorPrefix(rtxn, apples, "g")

	// -----------------------------------------------------------------------------

	fmt.Println("\n--- Update the basket")

	prevApplesRevision := apples.Revision(rtxn)

	// Let's update the basket to add an apple.
	txn = db.WriteTxn(baskets, apples)

	yellow := newApple("yellow")
	apples.Insert(txn, yellow)

	// Look up the basket we want to modify using the write transaction. This makes
	// sure the basket does not change underneath. If we would be doing expensive
	// operations here, then one might want to consider either processing in smaller
	// batches or use optimistic concurrency control (use revision number to detect
	// conflicts and retry).
	basket, _, ok := baskets.First(txn, basketNameIndex.Query("small"))
	if !ok {
		panic("Oops, basket 'small' not found")
	}

	// Since data stored in the database must be immutable (since readers
	// access it without locks), we need to clone the basket first.
	// Due to this, it's important to keep the objects as Plain Old Data
	// (no mutating methods), small and use appropriate data structures
	// for efficient cloning.
	basket = basket.Clone()
	basket.Apples = append(basket.Apples, yellow.ID)
	baskets.Insert(txn, basket)

	txn.Commit()

	// Data is also indexed by revision, which we can use to figure out what changed.
	printApplesChangedAfterRevision(db.ReadTxn(), apples, prevApplesRevision)

	watch = printBaskets(db.ReadTxn(), baskets, apples)

	// -----------------------------------------------------------------------------

	fmt.Println("\n--- Delete baskets in the background")

	// Delete all baskets in the background
	go func() {
		txn := db.WriteTxn(baskets)
		defer txn.Commit()
		fmt.Println("Deleting all baskets")

		// DeleteAll(WriteTxn) error
		if err := baskets.DeleteAll(txn); err != nil {
			// Just like Insert(), Delete and DeleteAll can fail if the
			// write transaction was committed, or if the table was targeted.
			panic("Oops, DeleteAll failed")
		}

	}()

	// Wait for the deletions to take effect.
	<-watch

	// -----------------------------------------------------------------------------

	fmt.Println("\n--- Observe the deletions")

	// The delete tracker we created earlier can be used to collect deleted objects
	// since our last read.
	// func (dt *DeleteTracker[Obj]) Deleted(txn ReadTxn, minRevision Revision) Iterator[Obj]
	// func Collect[Obj any](iter Iterator[Obj]) []Obj
	fmt.Printf("Deleted baskets: %+v\n", statedb.Collect(dt.Deleted(db.ReadTxn(), 0)))

	// Tell the database that we've now processed these deletions and the deleted objects
	// can be released.
	// Revision(ReadTxn) Revision
	dt.Mark(rtxn, baskets.Revision(rtxn))
}

func printBaskets(txn statedb.ReadTxn, baskets statedb.Table[*Basket], apples statedb.Table[*Apple]) <-chan struct{} {
	fmt.Printf("Baskets:\n")
	// All(ReadTxn) (Iterator[Obj], <-chan struct{})
	iter, watchBaskets := baskets.All(txn)

	// Next() (obj Obj, rev Revision, ok bool)
	for basket, rev, ok := iter.Next(); ok; basket, rev, ok = iter.Next() {
		fmt.Printf("  %s (%d):\n", basket.Name, rev)
		for _, id := range basket.Apples {
			// First(ReadTxn, Query[Obj]) (obj Obj, rev Revision, found bool)
			// First returns the first matching object for the given query.
			// (there may be many with non-unique indexes)
			if apple, _, ok := apples.First(txn, appleIDIndex.Query(id)); ok {
				fmt.Printf("  ... %s (%s)\n", apple.ID, apple.Color)
			}
		}
	}
	return watchBaskets
}

func printApplesOfColor(txn statedb.ReadTxn, apples statedb.Table[*Apple], color string) {
	fmt.Printf("Apples with color %q:\n", color)
	// Get(ReadTxn, Query[Obj]) (Iterator[Obj], <-chan struct{})
	// Get returns all matching objects for given query.
	iter, _ := apples.Get(txn, appleColorIndex.Query(color))

	for apple, _, ok := iter.Next(); ok; apple, _, ok = iter.Next() {
		fmt.Printf("  %s (%s)\n", apple.ID, apple.Color)
	}
}

func printApplesWithColorPrefix(txn statedb.ReadTxn, apples statedb.Table[*Apple], prefix string) {
	fmt.Printf("Apples with color prefix %q:\n", prefix)

	// Using the "LowerBound" search, we can search items by prefix.
	// LowerBound(ReadTxn, Query[Obj]) (iter Iterator[Obj], watch <-chan struct{})
	iter, _ := apples.LowerBound(txn, appleColorIndex.Query(prefix))

	for apple, _, ok := iter.Next(); ok; apple, _, ok = iter.Next() {
		if !strings.HasPrefix(apple.Color, prefix) {
			break
		}
		fmt.Printf("  %s (%s)\n", apple.ID, apple.Color)
	}
}

func printApplesChangedAfterRevision(txn statedb.ReadTxn, apples statedb.Table[*Apple], revision statedb.Revision) {
	fmt.Printf("Apples changed after %d:\n", revision)
	iter, _ := apples.LowerBound(txn, statedb.ByRevision[*Apple](revision+1))
	for apple, revision, ok := iter.Next(); ok; apple, revision, ok = iter.Next() {
		fmt.Printf("  %s (color: %s, revision: %d)\n", apple.ID, apple.Color, revision)
	}
}
