package policy

import (
	"fmt"
	"testing"
	"time"

	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/policy/api"
	"github.com/cilium/statedb"
	"github.com/stretchr/testify/require"
)

func TestSelectorTable(t *testing.T) {
	db := statedb.New()
	tbl, err := NewSelectorTable(db)
	require.NoError(t, err)

	wtxn := db.WriteTxn(tbl)

	testSelector := api.NewESFromLabels(labels.NewLabel("app", "test", labels.LabelSourceAny))
	testSelector2 := api.NewESFromLabels(labels.NewLabel("app", "test2", labels.LabelSourceAny))

	tbl.Insert(wtxn, &Selector{
		EndpointSelector: testSelector,
		Selections:       []identity.NumericIdentity{1, 3},
	})
	tbl.Insert(wtxn, &Selector{
		EndpointSelector: testSelector2,
		Selections:       []identity.NumericIdentity{2, 3, 4},
	})
	wtxn.Commit()

	// Take a snapshot of the state.
	txn := db.ReadTxn()

	// Get all selectors that referenced id 2
	id := identity.NumericIdentity(2)
	iter, watch := tbl.ListWatch(txn, SelectorsByIdentity(id))

	for sel, rev, ok := iter.Next(); ok; sel, rev, ok = iter.Next() {
		fmt.Printf("1) %d -> %v (revision: %d)\n", id, sel, rev)
	}

	go func() {
		time.Sleep(time.Second)
		fmt.Printf("add id 2 to test\n")
		wtxn = db.WriteTxn(tbl)
		tbl.Insert(wtxn, &Selector{
			EndpointSelector: testSelector,
			Selections:       []identity.NumericIdentity{1, 2, 3},
		})
		wtxn.Commit()
	}()

	// Wait until the selections for [id] change.
	// (this may close on unrelated changes depending on radix tree structure)
	<-watch

	// Check again
	txn = db.ReadTxn()
	iter, watch = tbl.ListWatch(txn, SelectorsByIdentity(id))
	for sel, rev, ok := iter.Next(); ok; sel, rev, ok = iter.Next() {
		fmt.Printf("2) %d -> %v (revision: %d)\n", id, sel, rev)
	}
}
