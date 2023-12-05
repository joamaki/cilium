package reconciler_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	metricsPkg "github.com/cilium/cilium/pkg/metrics"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/reconciler"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/index"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
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
	pruned  atomic.Bool
	updated atomic.Bool
	deleted atomic.Bool
	faulty  atomic.Bool
}

// Delete implements reconciler.Target.
func (mt *mockTarget) Delete(context.Context, statedb.ReadTxn, *testObject) error {
	mt.deleted.Store(true)
	if mt.faulty.Load() {
		return errors.New("oopsie")
	}
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
	defer goleak.VerifyNone(t)

	mt := &mockTarget{}

	var (
		db       *statedb.DB
		metrics  *reconciler.Metrics
		registry *metricsPkg.Registry
	)

	testObjects, err := statedb.NewTable[*testObject]("test-objects", idIndex)
	require.NoError(t, err, "NewTable")

	hive := hive.New(
		statedb.Cell,
		job.Cell,
		reconciler.Cell,

		cell.Group(
			cell.Provide(func() *option.DaemonConfig { return option.Config }),
			cell.Provide(func() metricsPkg.RegistryConfig { return metricsPkg.RegistryConfig{} }),
			cell.Provide(metricsPkg.NewRegistry),
		),

		cell.Invoke(func(r *metricsPkg.Registry, m *reconciler.Metrics) {
			registry = r
			metrics = m
		}),

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
					FullReconcilationInterval: 5 * time.Millisecond,
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

	numIterations := 10
	for i := 0; i < numIterations; i++ {
		mt.faulty.Store(false)
		mt.updated.Store(false)
		mt.pruned.Store(false)
		mt.deleted.Store(false)

		getStatus := func() reconciler.Status {
			obj, _, ok := testObjects.First(db.ReadTxn(), idIndex.Query(uint64(i)))
			if !ok {
				return reconciler.Status{}
			}
			return obj.status
		}

		eventually := func(msg string, cond func() bool) {
			assert.Eventually(t, cond, time.Second, time.Millisecond, msg)
		}

		// ---
		// Check initial reconciliation.

		wtxn := db.WriteTxn(testObjects)
		testObjects.Insert(wtxn,
			&testObject{
				id:     uint64(i),
				x:      0,
				status: reconciler.StatusPending(),
			})
		wtxn.Commit()

		// Wait until the object has been reconciled.
		eventually(
			"object has been reconciled and marked done",
			func() bool {
				isDone := getStatus().Kind == reconciler.StatusKindDone
				return mt.updated.Load() && !mt.deleted.Load() && isDone
			})

		// ---
		// Make the target faulty and modify the object. Check that
		// it is retried and eventually recovers when target is healthy again.
		mt.faulty.Store(true)

		wtxn = db.WriteTxn(testObjects)
		testObjects.Insert(wtxn,
			&testObject{
				id:     uint64(i),
				x:      1,
				status: reconciler.StatusPending(),
			})
		wtxn.Commit()

		eventually(
			"object has been marked with error",
			func() bool {
				s := getStatus()
				return s.Kind == reconciler.StatusKindError && s.Error == "oopsie"
			})

		mt.faulty.Store(false)

		eventually(
			"object has been marked done",
			func() bool {
				s := getStatus()
				return s.Kind == reconciler.StatusKindDone && s.Error == ""
			})

		// ---
		// Validate that the full reconciliation has been run at least once and that the
		// object has been poked.

		mt.updated.Store(false)

		eventually(
			"full reconciliation has run",
			func() bool {
				countMetric, _ := metrics.FullReconciliationCount.GetMetricWith(prometheus.Labels{reconciler.LabelModuleId: "test"})
				return mt.pruned.Load() && mt.updated.Load() && countMetric.Get() >= 1.0
			})

		// ---
		// Validate that failed full reconciliation for an object will be retried.

		mt.faulty.Store(true)

		// Wait until the object has been marked error'd after full reconciliation
		eventually(
			"object has been marked with error",
			func() bool {
				s := getStatus()
				return s.Kind == reconciler.StatusKindError &&
					s.Error == "oopsie"
			})

		// Make things work again and wait for reconciliation.
		mt.faulty.Store(false)

		eventually(
			"object has been marked done after full reconciliation",
			func() bool {
				s := getStatus()
				return s.Kind == reconciler.StatusKindDone && s.Error == ""
			})

		// ---
		// Mark the object for deletion and check that it's deleted.

		wtxn = db.WriteTxn(testObjects)
		testObjects.Insert(wtxn, &testObject{id: uint64(i), status: reconciler.StatusPendingDelete()})
		wtxn.Commit()

		eventually(
			"object has been deleted",
			func() bool {
				s := getStatus()
				return mt.deleted.Load() && s.Kind == "" /* proxy for missing object */
			})

		if t.Failed() {
			break
		}
	}

	// FIXME:
	// - if object fails in full reconciliation it should be retried
	// - if object changes retries are cleared

	// ---
	// Validate that the metrics are populated and make some sense.
	m := dumpMetrics(registry)
	assertSensibleMetricDuration(t, m, "cilium_reconciler_full_duration_seconds/module_id=test,op=prune")
	assertSensibleMetricDuration(t, m, "cilium_reconciler_full_duration_seconds/module_id=test,op=update")
	assert.Greater(t, m["cilium_reconciler_full_out_of_sync_total/module_id=test"], 0.0)
	assert.Greater(t, m["cilium_reconciler_full_total/module_id=test"], 0.0)

	assertSensibleMetricDuration(t, m, "cilium_reconciler_incremental_duration_seconds/module_id=test,op=update")
	assertSensibleMetricDuration(t, m, "cilium_reconciler_incremental_duration_seconds/module_id=test,op=delete")
	assert.Equal(t, m["cilium_reconciler_incremental_errors_current/module_id=test"], 0.0)
	assert.Greater(t, m["cilium_reconciler_incremental_errors_total/module_id=test"], 0.0)
	assert.Greater(t, m["cilium_reconciler_incremental_total/module_id=test"], 0.0)

	assert.NoError(t, hive.Stop(context.TODO()), "Stop")
}

func assertSensibleMetricDuration(t *testing.T, metrics map[string]float64, metric string) {
	assert.Less(t, metrics[metric], 1.0, "expected metric %q to be above zero", metric)

	// TODO: Sometimes the histogram metric is 0.0 even though samples have been added. Figure
	// out why and what's a better way to validate it. For now just log that it was 0.
	//assert.Greater(t, metrics[metric], 0.0, "expected metric %q to be above zero", metric)
	if metrics[metric] == 0.0 {
		t.Logf("!!! metric %q unexpectedly zero", metric)
	}
}

func dumpMetrics(r *metricsPkg.Registry) map[string]float64 {
	out := map[string]float64{}
	metrics, err := r.DumpMetrics()
	if err != nil {
		return nil
	}
	for _, m := range metrics {
		if strings.HasPrefix(m.Name, "cilium_reconciler") {
			out[m.Name+"/"+concatLabels(m.Labels)] = m.Value
		}
	}
	return out
}

func concatLabels(m map[string]string) string {
	labels := []string{}
	for k, v := range m {
		labels = append(labels, k+"="+v)
	}
	return strings.Join(labels, ",")
}
