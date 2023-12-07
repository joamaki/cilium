package reconciler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/statedb"
)

// Register creates a new reconciler and registers to the application
// lifecycle. To be used with cell.Invoke when the API of the reconciler
// is not needed.
func Register[Obj comparable](p params[Obj]) {
	New(p)
}

// New creates and registers a new reconciler.
func New[Obj comparable](p params[Obj]) Reconciler[Obj] {
	r := &reconciler[Obj]{
		params:              p,
		retries:             newRetries(p.Config.RetryBackoffMinDuration, p.Config.RetryBackoffMaxDuration),
		externalFullTrigger: make(chan struct{}, 1),
		labels: prometheus.Labels{
			LabelModuleId: string(p.ModuleId),
		},
	}

	g := p.Jobs.NewGroup(p.Scope)

	g.Add(job.OneShot("reconciler-loop", r.loop, job.WithAlwaysRetry()))
	p.Lifecycle.Append(g)

	return r
}

type params[Obj comparable] struct {
	cell.In

	Config    Config[Obj]
	Lifecycle hive.Lifecycle
	Log       logrus.FieldLogger
	DB        *statedb.DB
	Table     statedb.RWTable[Obj]
	Target    Target[Obj]
	Jobs      job.Registry
	Metrics   *Metrics
	ModuleId  cell.ModuleID
	Scope     cell.Scope
}

type reconciler[Obj comparable] struct {
	params[Obj]

	retries             *retries
	externalFullTrigger chan struct{}
	labels              prometheus.Labels
}

func (r *reconciler[Obj]) TriggerFullReconciliation() {
	select {
	case r.externalFullTrigger <- struct{}{}:
	default:
	}
}

func (r *reconciler[Obj]) loop(ctx context.Context, health cell.HealthReporter) error {
	fullReconTicker := time.NewTicker(r.Config.FullReconcilationInterval)
	defer fullReconTicker.Stop()

	tableWatchChan := closedWatchChannel
	revision := statedb.Revision(0)
	fullReconciliation := false

	for {
		// Wait for trigger
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-fullReconTicker.C:
			fullReconciliation = true
		case <-r.externalFullTrigger:
			fullReconciliation = true

		case <-r.retries.Wait():

		case <-tableWatchChan:
		}

		txn := r.DB.ReadTxn()

		var (
			err  error
			errs []error
		)

		// Perform incremental reconciliation and retries of previously failed
		// objects.
		revision, tableWatchChan, err = r.incremental(ctx, txn, revision)
		if err != nil {
			errs = append(errs, err)
		}

		if fullReconciliation {
			// Time to perform a full reconciliation. An incremental reconciliation
			// has been performed prior to this, so the assumption is that everything
			// is up to date (provided incremental reconciliation did not fail). We
			// report full reconciliation disparencies as they're indicative of something
			// interfering with Cilium operations.

			// Clear full reconciliation even if there's errors. Individual objects
			// will be retried via the retry queue.
			fullReconciliation = false

			var err error
			revision, err = r.full(ctx, txn, revision)
			if err != nil {
				errs = append(errs, err)
			}
		}

		// TODO: stats in health, e.g. number of objects and so on.
		if len(errs) == 0 {
			health.OK(fmt.Sprintf("OK, %d objects", r.Table.NumObjects(txn)))
		} else {
			health.Degraded("Reconciliation failed", errors.Join(errs...))
		}
	}
}

type targetOpResult struct {
	rev    statedb.Revision
	status Status
}

func (r *reconciler[Obj]) incremental(
	ctx context.Context,
	rtxn statedb.ReadTxn,
	lastRev statedb.Revision,
) (statedb.Revision, <-chan struct{}, error) {
	// In order to not lock the table while doing potentially expensive operations,
	// we collect the reconciliation statuses for the objects and then commit them
	// afterwards. If the object has changed in the meanwhile the status update for
	// the old object is skipped.
	updateResults := make(map[Obj]targetOpResult)
	toBeDeleted := make(map[Obj]statedb.Revision)
	errs := []error{}

	// Function to process the new&changed objects and retries.
	process := func(key []byte, obj Obj, rev statedb.Revision) error {
		start := time.Now()

		status := r.Config.GetObjectStatus(obj)

		// Ignore objects that have already been marked as reconciled.
		if status.Kind == StatusKindDone {
			return nil
		}

		labels := maps.Clone(r.labels)
		var err error
		if status.Delete {
			labels[LabelOperation] = OpDelete
			err = r.Target.Delete(ctx, rtxn, obj)
			if err == nil {
				toBeDeleted[obj] = rev
			} else {
				updateResults[obj] = targetOpResult{rev, StatusError(true, err)}
			}
		} else {
			labels[LabelOperation] = OpUpdate
			_, err = r.Target.Update(ctx, rtxn, obj)
			if err == nil {
				updateResults[obj] = targetOpResult{rev, StatusDone()}
			} else {
				updateResults[obj] = targetOpResult{rev, StatusError(false, err)}
			}
		}
		r.Metrics.IncrementalReconciliationDuration.With(labels).Observe(
			float64(time.Since(start)) / float64(time.Second),
		)

		if err != nil {
			// Reconciling the object failed, so add it to be retried.
			r.retries.Add(key)
		} else {
			// Reconciling succeeded, so clear the object.
			r.retries.Clear(key)
		}

		return err
	}

	// Iterate in revision order through new and changed objects.
	newRevision := lastRev
	iter, watch := r.Table.LowerBound(rtxn, statedb.ByRevision[Obj](lastRev+1))
	for obj, rev, ok := iter.Next(); ok; obj, rev, ok = iter.Next() {
		newRevision = rev

		status := r.Config.GetObjectStatus(obj)
		if status.Kind != StatusKindPending {
			// Only process objects that are pending reconciliation, e.g.
			// changed from outside.
			// Failures (e.g. StatusKindError) are processed via the retry queue.
			continue
		}

		key := r.Table.PrimaryKey(obj)

		// Clear an existing retry as the object has changed.
		r.retries.Clear(key)

		err := process(key, obj, rev)
		if err != nil {
			// FIXME: This probably becomes too big!
			errs = append(errs, err)
		}
	}

	// Process objects that are ready to be retried.
	for {
		key, retryAt, ok := r.retries.Top()
		if !ok || retryAt.After(time.Now()) {
			break
		}
		r.retries.Pop()

		obj, rev, ok := r.Table.GetByPrimaryKey(rtxn, key)
		if !ok {
			// Object has been deleted unexpectedly (e.g. from outside
			// the reconciler). Shrug and carry on.
			continue
		}

		kind := r.Config.GetObjectStatus(obj).Kind
		if kind == StatusKindDone {
			// Object does not need reconciliation.
			continue
		}

		err := process(key, obj, rev)
		if err != nil {
			errs = append(errs, err)
		}
	}

	// Commit status updates.
	{
		wtxn := r.DB.WriteTxn(r.Table)

		oldRev := r.Table.Revision(rtxn)
		newRev := r.Table.Revision(wtxn)

		// Commit status for updated objects.
		for obj, result := range updateResults {
			// Update the object if it is unchanged. It may happen that the object has
			// been updated in the meanwhile, in which case we ignore the status as the
			// update will be picked up by next reconciliation round.
			r.Table.CompareAndSwap(wtxn, result.rev, r.Config.WithObjectStatus(obj, result.status))
		}

		// Delete the objects that had been successfully deleted from target.
		// The object is only deleted if it has not been changed.
		for obj, rev := range toBeDeleted {
			r.Table.CompareAndDelete(wtxn, rev, obj)
		}

		if oldRev == newRev {
			// No changes happened between the ReadTxn and this WriteTxn. Since
			// we wrote the table the 'watch' channel has closed. Grab a new
			// watch channel of the root to only watch for new changes after
			// this write.
			_, watch = r.Table.All(wtxn)
		}

		wtxn.Commit()
	}

	r.Metrics.IncrementalReconciliationTotalErrors.With(r.labels).Add(float64(len(errs)))
	r.Metrics.IncrementalReconciliationCurrentErrors.With(r.labels).Set(float64(len(errs)))
	r.Metrics.IncrementalReconciliationCount.With(r.labels).Add(1)

	return newRevision, watch, fmt.Errorf("incremental: %w", errors.Join(errs...))
}

func (r *reconciler[Obj]) full(ctx context.Context, txn statedb.ReadTxn, lastRev statedb.Revision) (statedb.Revision, error) {
	defer r.Metrics.FullReconciliationCount.With(r.labels).Add(1)

	var errs []error
	outOfSync := false

	// First perform pruning to make room in the target.
	iter, _ := r.Table.All(txn)
	start := time.Now()
	if err := r.Target.Prune(ctx, txn, iter); err != nil {
		outOfSync = true
		errs = append(errs, fmt.Errorf("pruning failed: %w", err))
	}
	labels := maps.Clone(r.labels)
	labels[LabelOperation] = OpPrune
	r.Metrics.FullReconciliationDuration.With(labels).Observe(
		float64(time.Since(start)) / float64(time.Second),
	)

	// Call Update() for each desired object to validate that it is up-to-date.
	updateResults := make(map[Obj]targetOpResult)
	updateErrors := []error{}  // TODO slight waste of space
	iter, _ = r.Table.All(txn) // Grab a new iterator as Prune() may have consumed it.
	for obj, rev, ok := iter.Next(); ok; obj, rev, ok = iter.Next() {
		start := time.Now()
		changed, err := r.Target.Update(ctx, txn, obj)

		labels := maps.Clone(r.labels)
		labels[LabelOperation] = OpUpdate
		r.Metrics.FullReconciliationDuration.With(labels).Observe(
			float64(time.Since(start)) / float64(time.Second),
		)

		outOfSync = outOfSync || changed
		if err == nil {
			updateResults[obj] = targetOpResult{rev, StatusDone()}
			r.retries.Clear(r.Table.PrimaryKey(obj))
		} else {
			updateResults[obj] = targetOpResult{rev, StatusError(false, err)}
			updateErrors = append(updateErrors, err)
		}
	}

	// Increment the out-of-sync counter if full reconciliation catched any out-of-sync
	// objects.
	if outOfSync {
		r.Metrics.FullReconciliationOutOfSyncCount.With(r.labels).Add(1)
	}

	// Take a sample of the update errors if any. Only taking first one as there
	// maybe thousands of errors. The per-object errors will be stored in the desired
	// state.
	if len(updateErrors) > 0 {
		errs = append(errs,
			fmt.Errorf("%d update errors, first errors: %w",
				len(updateErrors), updateErrors[0]))
	}

	// Commit the new desired object status. This is performed separately in order
	// to not lock the table when performing long-running target operations.
	// If the desired object has been updated in the meanwhile the status update is dropped.
	if len(updateResults) > 0 {
		wtxn := r.DB.WriteTxn(r.Table)
		for obj, result := range updateResults {
			obj = r.Config.WithObjectStatus(obj, result.status)
			_, _, err := r.Table.CompareAndSwap(wtxn, result.rev, obj)
			if err == nil && result.status.Kind != StatusKindDone {
				// Object had not changed in the meantime, queue the retry.
				r.retries.Add(r.Table.PrimaryKey(obj))
			}
		}
		wtxn.Commit()
	}

	if len(errs) > 0 {
		r.Metrics.FullReconciliationTotalErrors.With(r.labels).Add(1)
		return r.Table.Revision(txn), fmt.Errorf("full: %w", errors.Join(errs...))
	}

	// Sync succeeded up to latest revision. Continue incremental reconciliation from
	// this revision.
	return r.Table.Revision(txn), nil
}

var closedWatchChannel = func() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()
