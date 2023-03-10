package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/rate"
	"github.com/cilium/cilium/pkg/stream"
)

// TODO: What to name this
var Cell = cell.Module(
	"controller-identity",
	"Endpoint identity allocation",
	cell.Invoke(registerAllocator),
)

func registerAllocator(lc hive.Lifecycle, log logrus.FieldLogger, s *state.State, shutdowner hive.Shutdowner) {
	d := &allocator{state: s, log: log, shutdowner: shutdowner}
	lc.Append(d)
}

type allocator struct {
	ctx    context.Context
	cancel context.CancelFunc
	eg     *errgroup.Group

	shutdowner hive.Shutdowner
	state      *state.State
	log        logrus.FieldLogger

	numericalIdentity uint32
}

func (d *allocator) Start(hive.HookContext) (err error) {
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.eg, _ = errgroup.WithContext(d.ctx)
	if err != nil {
		return
	}
	d.eg.Go(d.loop)

	go func() {
		err := d.eg.Wait()
		if err != nil {
			d.shutdowner.Shutdown(hive.ShutdownWithError(err))
		}
	}()

	return nil
}

func (d *allocator) Stop(hive.HookContext) error {
	d.cancel()
	return nil
}

func (d *allocator) loop() error {
	// Limit to processing policies to 20 per second
	// TODO configurable
	lim := rate.NewLimiter(time.Second, 20)
	defer lim.Stop()

	// The rules have a revision number corresponding to the import round of the data source.
	revision := uint64(0)

	d.log.Info("Identity allocator running")

	// go-memdb doesn't allow watching LowerBound(), so instead we're
	// triggering on any changes to endpoints table here.
	endpointsChanged :=
		stream.Trigger(
			d.ctx,
			stream.Filter(d.state.Observable, state.Event.ForEndpointTable))

	for {
		// TODO: Doing this as one large transaction, but this
		// could also be split up into batches if necessary, e.g.
		// only process N changed endpoints at a time.
		tx := d.state.WriteTx()

		// Get new and changed rules with revisions higher than the previously processed.
		// TODO only process endpoints in the EPInit state?
		epIter, err := tx.Endpoints().LowerBound(state.ByRevision(revision))
		if err != nil {
			// TODO: This can only fail on bad queries, e.g. missing
			// indices and stuff. We should trigger a shutdown.
			return fmt.Errorf("Endpoints().LowerBound failed: %w", err)
		}

		err = state.ProcessEach(
			epIter,
			func(ep *state.Endpoint) error {
				d.log.WithField("name", ep.Name).Info("Processing endpoint")

				// Remember the highest seen revision for the next round.
				if ep.Revision > revision {
					revision = ep.Revision
				}
				return d.processEndpoint(tx, ep)
			},
		)
		if err == nil {
			if err := tx.Commit(); err != nil {
				// Commit could fail if the reflector says no, e.g. due to
				// persisting of some table failing or unallowed modifications made.
				// Unclear how to handle this, e.g. should failures to persist to
				// external systems bubble up here or should those be handled by the
				// persistence layer?
				return fmt.Errorf("state commit failed: %w", err)
			}
		} else {
			d.log.WithError(err).Error("Endpoint processing failed")
			tx.Abort()
		}

		d.log.Info("Done, waiting for changes.")

		select {
		case <-d.ctx.Done():
			d.log.Info("Context closed!")
			return nil
		case <-endpointsChanged:
			// New changes, wait a bit before the next round.
			d.log.Info("Rate limiting")
			lim.Wait(d.ctx)
		}
	}
}

func (d *allocator) processEndpoint(tx *state.StateTx, ep *state.Endpoint) error {
	switch ep.State {
	case state.EPInit:
		// Make a copy so we can update it.
		ep = ep.Copy()
		ep.Revision += 1
		ep.State = state.EPProcessing

		// Find whether a matching SelectorPolicy exists.
		sp, err := tx.SelectorPolicies().First(state.ByLabelKey(ep.LabelKey))
		if err != nil {
			d.log.WithError(err).Error("SelectorPolicies.First")
			return err
		}

		if sp != nil {
			// A matching identity and policy found.
			ep.SelectorPolicyID = sp.ID
		} else {
			// New identity!
			sp = &state.SelectorPolicy{
				ID:              state.NewUUID(),
				NumericIdentity: d.numericalIdentity,
				LabelKey:        ep.LabelKey,
				Labels:          ep.Labels.IdentityLabels().LabelArray(),
				Revision:        1, // TODO semantics of SP revision
			}
			if err := tx.SelectorPolicies().Insert(sp); err != nil {
				d.log.WithError(err).Error("SelectorPolicies.Insert")
				return err
			}

			d.log.Infof("Allocated new identity: %d for labels %s", sp.NumericIdentity, sp.LabelKey)
			ep.SelectorPolicyID = sp.ID
			d.numericalIdentity++
		}

		return tx.Endpoints().Insert(ep)
	default:
		return nil
	}

}
