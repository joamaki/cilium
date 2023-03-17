package controllers

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/cilium/cilium/memdb/datapath/maps"
	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/memdb/state/structs"
	"github.com/cilium/cilium/memdb/tables/services"
	"github.com/cilium/cilium/pkg/backoff"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/rate"
	"github.com/cilium/cilium/pkg/stream"
)

var Cell = cell.Module(
	"datapath-controller-services",
	"Synchronizes the service map",
	cell.Invoke(registerServicesController),
)

type params struct {
	cell.In

	Lifecycle hive.Lifecycle
	Log       logrus.FieldLogger

	State    *state.State
	Services state.Table[*services.Service]

	ServiceMap *maps.ServiceMap
}

func registerServicesController(p params) {
	c := &controller{params: p}
	p.Lifecycle.Append(c)
}

type controller struct {
	params

	ctx    context.Context
	cancel context.CancelFunc
}

func (c *controller) Start(hive.HookContext) (err error) {
	c.ctx, c.cancel = context.WithCancel(context.Background())
	go c.loop()
	return nil
}

func (c *controller) Stop(hive.HookContext) error {
	c.cancel()
	return nil
}

func (c *controller) updateServiceState(svcID structs.UUID, oldState, newState services.ServiceState) {
	// Update the service state using a small transaction. This makes sure the potentially
	// slow operation of updating the map is not blocking other writers.
	tx := c.State.Write()
	svcs := c.Services.Modify(tx)
	if svc, err := svcs.First(state.ByID(svcID)); err == nil && svc != nil {
		// Only update if the service was not updated in between.
		if svc.State == oldState {
			svc = svc.DeepCopy()
			svc.State = newState
			svcs.Insert(svc)
		}
	}
	tx.Commit()
}

func (c *controller) loop() {
	lim := rate.NewLimiter(time.Second, 20)
	defer lim.Stop()

	backoff := backoff.Exponential{
		Min: 10 * time.Second,
		Max: time.Minute,
	}

	nextRevision := uint64(0)

	servicesChanged :=
		stream.Trigger(
			c.ctx,
			stream.Filter(c.State.Observable,
				func(e state.Event) bool {
					return e.ForTable(c.Services.Name())
				}))

	for {
		// Do a read transaction on the services to apply the changes without blocking writers.
		// This does mean the modifications we do will need to take into account any changes
		// that might have happened in the mean time.
		svcs := c.Services.Read(c.State.Read())

		// Get new and changed services.
		iter, err := svcs.LowerBound(state.ByRevision(nextRevision))
		if err != nil {
			// TODO: This can only fail on bad queries, e.g. missing
			// indices and stuff. Likely want to panic.
			panic(err)
		}

		newRevision := nextRevision

		err = state.ProcessEach(
			iter,
			func(svc *services.Service) error {
				// Remember the highest seen revision for the next round.
				if newRevision < svc.Revision {
					newRevision = svc.Revision
				}

				// Ignore already processed services.
				if svc.State == services.ServiceStateApplied {
					return nil
				}

				if err := c.processService(svc); err != nil {
					c.Log.WithField("name", svc.Name).
						WithField("namespace", svc.Namespace).
						WithError(err).
						Error("Failed to process service")
					c.updateServiceState(svc.ID, svc.State, services.ServiceStateFailure)
					return err
				}
				c.updateServiceState(svc.ID, svc.State, services.ServiceStateApplied)
				c.Log.WithField("name", svc.Name).WithField("namespace", svc.Namespace).Info("Upserted service to ServiceMap")
				return nil
			},
		)

		if err == nil {
			nextRevision = newRevision
			backoff.Reset()
		} else {
			c.Log.Info("Failures when processing services. Retrying.")
			backoff.Wait(c.ctx)
		}

		select {
		case <-c.ctx.Done():
			return
		case <-servicesChanged:
			lim.Wait(c.ctx)
		}
	}
}

func (c *controller) processService(svc *services.Service) error {
	return c.ServiceMap.Upsert(
		// make up some data:
		maps.ServiceKey(svc.Name), maps.ServiceValue(svc.Namespace),
	)

}

/*
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

}*/
