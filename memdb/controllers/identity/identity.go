package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/memdb/state/structs"
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

type allocatorParams struct {
	cell.In

	Lifecycle  hive.Lifecycle
	Log        logrus.FieldLogger
	Shutdowner hive.Shutdowner

	State            *state.State
	Endpoints        state.Table[*structs.Endpoint]
	SelectorPolicies state.Table[*structs.SelectorPolicy]
}

func registerAllocator(p allocatorParams) {
	d := &allocator{allocatorParams: p}
	p.Lifecycle.Append(d)
}

type allocator struct {
	allocatorParams

	ctx    context.Context
	cancel context.CancelFunc
	eg     *errgroup.Group

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
			d.Shutdowner.Shutdown(hive.ShutdownWithError(err))
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

	d.Log.Info("Identity allocator running")

	// go-memdb doesn't allow watching LowerBound(), so instead we're
	// triggering on any changes to endpoints table here.
	endpointsChanged :=
		stream.Trigger(
			d.ctx,
			stream.Filter(d.State.Observable, state.Event.ForEndpointTable))

	for {
		// TODO: Doing this as one large transaction, but this
		// could also be split up into batches if necessary, e.g.
		// only process N changed endpoints at a time.
		tx := d.State.Write()
		endpoints := d.Endpoints.Modify(tx)
		selectorPolicies := d.SelectorPolicies.Modify(tx)

		// Get new and changed rules with revisions higher than the previously processed.
		// TODO only process endpoints in the EPInit state?
		epIter, err := endpoints.LowerBound(state.ByRevision(revision))
		if err != nil {
			// TODO: This can only fail on bad queries, e.g. missing
			// indices and stuff. We should trigger a shutdown.
			return fmt.Errorf("Endpoints().LowerBound failed: %w", err)
		}

		err = state.ProcessEach(
			epIter,
			func(ep *structs.Endpoint) error {
				d.Log.WithField("name", ep.Name).Info("Processing endpoint")

				// Remember the highest seen revision for the next round.
				if ep.Revision > revision {
					revision = ep.Revision
				}
				return d.processEndpoint(endpoints, selectorPolicies, ep)
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
			d.Log.WithError(err).Error("Endpoint processing failed")
			tx.Abort()
		}

		d.Log.Info("Done, waiting for changes.")

		select {
		case <-d.ctx.Done():
			d.Log.Info("Context closed!")
			return nil
		case <-endpointsChanged:
			// New changes, wait a bit before the next round.
			d.Log.Info("Rate limiting")
			lim.Wait(d.ctx)
		}
	}
}

func (d *allocator) processEndpoint(endpoints state.TableReaderWriter[*structs.Endpoint], selectorPolicies state.TableReaderWriter[*structs.SelectorPolicy], ep *structs.Endpoint) error {
	switch ep.State {
	case structs.EPInit:
		// Make a copy so we can update it.
		ep = ep.DeepCopy()
		ep.Revision += 1
		ep.State = structs.EPProcessing

		// Find whether a matching SelectorPolicy exists.
		sp, err := selectorPolicies.First(state.ByLabelKey(ep.LabelKey))
		if err != nil {
			d.Log.WithError(err).Error("SelectorPolicies.First")
			return err
		}

		if sp != nil {
			// A matching identity and policy found.
			ep.SelectorPolicyID = sp.ID
		} else {
			// New identity!
			sp = &structs.SelectorPolicy{
				ID:              structs.NewUUID(),
				NumericIdentity: d.numericalIdentity,
				LabelKey:        ep.LabelKey,
				Labels:          ep.Labels.IdentityLabels().LabelArray(),
				Revision:        1, // TODO semantics of SP revision
			}
			if err := selectorPolicies.Insert(sp); err != nil {
				d.Log.WithError(err).Error("SelectorPolicies.Insert")
				return err
			}

			d.Log.Infof("Allocated new identity: %d for labels %s", sp.NumericIdentity, sp.LabelKey)
			ep.SelectorPolicyID = sp.ID
			d.numericalIdentity++
		}

		return endpoints.Insert(ep)
	default:
		return nil
	}

}
