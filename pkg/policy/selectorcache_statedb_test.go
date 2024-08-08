package policy

import (
	"testing"

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

	txn := db.WriteTxn(tbl)

	testSelector := api.NewESFromLabels(labels.NewLabel("app", "test", labels.LabelSourceAny))

	tbl.Insert(txn, &Selector{
		EndpointSelector: testSelector,
		Labels:           []labels.Label{},
		Selections:       []identity.NumericIdentity{1, 2, 3},
	})
	txn.Commit()

	selector, revision, watch, found := tbl.GetWatch(db.ReadTxn(), SelectorByIdentity(1))
	require.True(t, found)
	require.NotNil(t, selector)
	require.NotZero(t, revision)

	select {
	case <-watch:
		t.Fatalf("watch channel closed")
	default:
	}

	selector, revision, watch, found = tbl.GetWatch(db.ReadTxn(), SelectorByES(&testSelector))
	require.True(t, found)
	require.NotNil(t, selector)
	require.NotZero(t, revision)

	select {
	case <-watch:
		t.Fatalf("watch channel closed")
	default:
	}
}

func TestSelectorTable_CachedSelector(t *testing.T) {
	db := statedb.New()
	tbl, err := NewSelectorTable(db)
	require.NoError(t, err)

	testSelector := api.NewESFromLabels(labels.NewLabel("app", "test", labels.LabelSourceAny))

	txn := db.WriteTxn(tbl)
	tbl.Insert(txn, &Selector{
		EndpointSelector: testSelector,
		Labels:           []labels.Label{},
		Selections:       []identity.NumericIdentity{1, 2, 3},
	})
	txn.Commit()

	cs := GetDBCachedSelector(db.ReadTxn(), tbl, &testSelector)
	require.NotNil(t, cs)

	ts := cs.Get(db.ReadTxn())
	require.NotNil(t, ts)
	require.Len(t, ts.Selections, 3)

	// Update the selections.
	txn = db.WriteTxn(tbl)
	tbl.Insert(txn, &Selector{
		EndpointSelector: testSelector,
		Labels:           []labels.Label{},
		Selections:       []identity.NumericIdentity{1, 2, 3, 4},
	})
	txn.Commit()

	// Get() will requery the selector if it is out-of-date.
	ts = cs.Get(db.ReadTxn())
	require.NotNil(t, ts)
	require.Len(t, ts.Selections, 4)
}

func BenchmarkSelectorTable_CachedSelector(b *testing.B) {
	db := statedb.New()
	tbl, err := NewSelectorTable(db)
	require.NoError(b, err)

	txn := db.WriteTxn(tbl)

	testSelector := api.NewESFromLabels(labels.NewLabel("app", "test", labels.LabelSourceAny))

	tbl.Insert(txn, &Selector{
		EndpointSelector: testSelector,
		Labels:           []labels.Label{},
		Selections:       []identity.NumericIdentity{1, 2, 3},
	})
	txn.Commit()

	cs := GetDBCachedSelector(db.ReadTxn(), tbl, &testSelector)
	require.NotNil(b, cs)

	ts := cs.Get(db.ReadTxn())
	require.NotNil(b, ts)
	require.Len(b, ts.Selections, 3)

	b.ResetTimer()

	rtxn := db.ReadTxn()
	for n := b.N; n > 0; n-- {
		ts := cs.Get(rtxn)
		if ts == nil {
			b.Fatalf("nil selector returned")
		}
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "gets/sec")
}

func BenchmarkSelectorTable_Uncached(b *testing.B) {
	db := statedb.New()
	tbl, err := NewSelectorTable(db)
	require.NoError(b, err)

	txn := db.WriteTxn(tbl)

	testSelector := api.NewESFromLabels(labels.NewLabel("app", "test", labels.LabelSourceAny))

	tbl.Insert(txn, &Selector{
		EndpointSelector: testSelector,
		Labels:           []labels.Label{},
		Selections:       []identity.NumericIdentity{1, 2, 3},
	})
	txn.Commit()

	rtxn := db.ReadTxn()

	b.ResetTimer()
	for n := b.N; n > 0; n-- {
		cs := GetDBCachedSelector(rtxn, tbl, &testSelector)
		if cs == nil {
			b.Fatalf("nil selector returned")
		}
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "gets/sec")
}
