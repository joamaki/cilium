package reconciler_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/reconciler"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testObject struct {
	id     uint64
	x      int
	faulty bool
	status reconciler.Status
}

var idIndex = statedb.Index[*testObject, uint64]{
	Name: "id",
	FromObject: func(t *testObject) index.KeySet {
		return index.NewKeySet(index.Uint64(t.id))
	},
	FromKey: index.Uint64,
	Unique:  true,
}

// GetStatus implements reconciler.Reconcilable.
func (t *testObject) GetStatus() reconciler.Status {
	return t.status
}

// WithStatus implements reconciler.Reconcilable.
func (t *testObject) WithStatus(status reconciler.Status) *testObject {
	t2 := *t
	t2.status = status
	return &t2
}

type mockTarget struct {
	initialized atomic.Bool
	pruned      atomic.Bool
	updated     atomic.Bool
	deleted     atomic.Bool
	faulty      atomic.Bool
}

// Delete implements reconciler.Target.
func (mt *mockTarget) Delete(context.Context, statedb.ReadTxn, *testObject) error {
	mt.deleted.Store(true)
	if mt.faulty.Load() {
		return errors.New("oopsie")
	}
	return nil
}

// Init implements reconciler.Target.
func (mt *mockTarget) Init(context.Context) error {
	mt.initialized.Store(true)
	return nil
}

// Prune implements reconciler.Target.
func (mt *mockTarget) Prune(context.Context, statedb.ReadTxn, statedb.Iterator[*testObject]) error {
	mt.pruned.Store(true)
	return nil
}

// Update implements reconciler.Target.
func (mt *mockTarget) Update(ctx context.Context, txn statedb.ReadTxn, o *testObject) (changed bool, err error) {
	mt.updated.Store(true)
	if mt.faulty.Load() || o.faulty {
		return true, errors.New("oopsie")
	}

	return true, nil
}

var _ reconciler.Target[*testObject] = &mockTarget{}

func TestReconciler(t *testing.T) {
	mt := &mockTarget{}

	var db *statedb.DB
	testObjects, err := statedb.NewTable[*testObject]("test-objects", idIndex)
	require.NoError(t, err, "NewTable")

	hive := hive.New(
		statedb.Cell,
		job.Cell,
		reconciler.Cell,

		cell.Module(
			"test",
			"Test",
			cell.Provide(func(db_ *statedb.DB) (statedb.RWTable[*testObject], error) {
				db = db_
				return testObjects, db.RegisterTable(testObjects)
			}),
			cell.Provide(
				func() (*mockTarget, reconciler.Target[*testObject]) {
					return mt, mt
				},
			),
			cell.Provide(func() reconciler.Config[*testObject] {
				return reconciler.Config[*testObject]{
					FullReconcilationInterval: time.Millisecond,
					RetryBackoffMinDuration:   time.Millisecond,
					RetryBackoffMaxDuration:   time.Millisecond,
					GetObjectStatus:           (*testObject).GetStatus,
					WithObjectStatus:          (*testObject).WithStatus,
				}
			}),
			cell.Invoke(reconciler.Register[*testObject]),
		),
	)

	require.NoError(t, hive.Start(context.TODO()), "Start")

	assert.Eventually(t, func() bool {
		return mt.initialized.Load()
	}, 100*time.Millisecond, time.Millisecond, "initialized")

	assert.Eventually(t, func() bool {
		return mt.pruned.Load()
	}, 100*time.Millisecond, time.Millisecond, "pruned")

	// ---

	wtxn := db.WriteTxn(testObjects)
	testObjects.Insert(wtxn,
		&testObject{
			id:     1,
			status: reconciler.StatusPending(),
		})
	wtxn.Commit()

	assert.Eventually(t, func() bool {
		return mt.updated.Load() && !mt.deleted.Load()
	}, 100*time.Millisecond, time.Millisecond, "updated")

	obj, _, ok := testObjects.First(db.ReadTxn(), idIndex.Query(1))
	if assert.True(t, ok) {
		assert.Equal(t, obj.status.Kind, reconciler.StatusKindDone)
	}

	// ---

	mt.faulty.Store(true)

	wtxn = db.WriteTxn(testObjects)
	testObjects.Insert(wtxn,
		&testObject{
			id:     1,
			x:      1,
			status: reconciler.StatusPending(),
		})
	wtxn.Commit()

	assert.Eventually(t, func() bool {
		return mt.updated.Load() && !mt.deleted.Load()
	}, 100*time.Millisecond, time.Millisecond, "updated")

	obj, _, ok = testObjects.First(db.ReadTxn(), idIndex.Query(1))
	if assert.True(t, ok) {
		assert.Equal(t, obj.status.Kind, reconciler.StatusKindError)
		assert.Equal(t, obj.status.Error, "oopsie")
	}

	mt.faulty.Store(false)

	assert.Eventually(t, func() bool {
		obj, _, ok = testObjects.First(db.ReadTxn(), idIndex.Query(1))
		if ok && obj.status.Kind == reconciler.StatusKindDone {
			assert.Equal(t, obj.status.Error, "")
			return true
		}
		return false
	}, 100*time.Millisecond, time.Millisecond, "updated")

	// ---

	wtxn = db.WriteTxn(testObjects)
	testObjects.Insert(wtxn, &testObject{id: 1, status: reconciler.StatusPendingDelete()})
	wtxn.Commit()

	assert.Eventually(t, func() bool {
		return mt.deleted.Load()
	}, 100*time.Millisecond, time.Millisecond, "deleted")

	require.NoError(t, hive.Stop(context.TODO()), "Stop")

	// FIXME:
	// - if object fails in full reconciliation it should be retried
	// - if object changes retries are cleared
}
