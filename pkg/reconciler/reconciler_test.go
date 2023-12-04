package reconciler_test

import (
	"context"
	"errors"
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
	initialized bool
	pruned      bool
	updated     bool
	deleted     bool

	faulty bool
}

// Delete implements reconciler.Target.
func (mt *mockTarget) Delete(context.Context, statedb.ReadTxn, *testObject) error {
	mt.deleted = true
	if mt.faulty {
		return errors.New("oopsie")
	}
	return nil
}

// Init implements reconciler.Target.
func (mt *mockTarget) Init(context.Context) error {
	mt.initialized = true
	return nil
}

// Prune implements reconciler.Target.
func (mt *mockTarget) Prune(context.Context, statedb.ReadTxn, statedb.Iterator[*testObject]) error {
	mt.pruned = true
	return nil
}

// Update implements reconciler.Target.
func (mt *mockTarget) Update(context.Context, statedb.ReadTxn, *testObject) (changed bool, err error) {
	mt.updated = true
	if mt.faulty {
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
			cell.Provide(func() reconciler.Config {
				return reconciler.Config{
					FullReconcilationInterval: time.Millisecond,
					RetryBackoffMinDuration:   time.Millisecond,
					RetryBackoffMaxDuration:   time.Millisecond,
				}
			}),
			cell.Invoke(reconciler.Register[*testObject]),
		),
	)

	require.NoError(t, hive.Start(context.TODO()), "Start")

	assert.Eventually(t, func() bool {
		return mt.initialized
	}, 100*time.Millisecond, time.Millisecond, "initialized")

	assert.Eventually(t, func() bool {
		return mt.pruned
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
		return mt.updated && !mt.deleted
	}, 100*time.Millisecond, time.Millisecond, "updated")

	obj, _, ok := testObjects.First(db.ReadTxn(), idIndex.Query(1))
	if assert.True(t, ok) {
		assert.Equal(t, obj.status.Kind, reconciler.StatusKindDone)
	}

	// ---

	mt.faulty = true // FIXME atomic

	wtxn = db.WriteTxn(testObjects)
	testObjects.Insert(wtxn,
		&testObject{
			id:     1,
			x:      1,
			status: reconciler.StatusPending(),
		})
	wtxn.Commit()

	assert.Eventually(t, func() bool {
		return mt.updated && !mt.deleted
	}, 100*time.Millisecond, time.Millisecond, "updated")

	obj, _, ok = testObjects.First(db.ReadTxn(), idIndex.Query(1))
	if assert.True(t, ok) {
		assert.Equal(t, obj.status.Kind, reconciler.StatusKindError)
		assert.Equal(t, obj.status.Error, "oopsie")
	}

	mt.faulty = false

	assert.Eventually(t, func() bool {
		return mt.updated && !mt.deleted
	}, 100*time.Millisecond, time.Millisecond, "updated")

	obj, _, ok = testObjects.First(db.ReadTxn(), idIndex.Query(1))
	if assert.True(t, ok) {
		assert.Equal(t, obj.status.Kind, reconciler.StatusKindDone)
		assert.Equal(t, obj.status.Error, "")
	}

	// ---

	wtxn = db.WriteTxn(testObjects)
	testObjects.Insert(wtxn, &testObject{id: 1, status: reconciler.StatusPendingDelete()})
	wtxn.Commit()

	assert.Eventually(t, func() bool {
		return mt.deleted
	}, 100*time.Millisecond, time.Millisecond, "deleted")

	require.NoError(t, hive.Stop(context.TODO()), "Stop")
}
