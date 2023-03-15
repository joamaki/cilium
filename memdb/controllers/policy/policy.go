package policy

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

var Cell = cell.Module(
	"controller-policy",
	"ExtPolicyRuly into L4Policies",
	cell.Invoke(registerDistillery),
)

type policyParams struct {
	cell.In

	Log logrus.FieldLogger

	State            *state.State
	ExtPolicyRules   state.Table[*structs.ExtPolicyRule]
	SelectorPolicies state.Table[*structs.SelectorPolicy]
}

func registerDistillery(lc hive.Lifecycle, params policyParams) {
	d := &distillery{policyParams: params}
	lc.Append(d)
}

type distillery struct {
	policyParams

	ctx    context.Context
	cancel context.CancelFunc
	eg     *errgroup.Group
}

func (d *distillery) Start(hive.HookContext) (err error) {
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.eg, _ = errgroup.WithContext(d.ctx)
	if err != nil {
		return
	}
	d.eg.Go(d.extPolicyRuleLoop)
	d.eg.Go(d.selectorPolicyLoop)
	return nil
}

func (d *distillery) Stop(hive.HookContext) error {
	d.cancel()
	// TODO: we likely want to Wait() earlier and catch unexpected errors
	// from distillLoop() and restart it. errgroup wrong abstraction.
	return d.eg.Wait()
}

func (d *distillery) extPolicyRuleLoop() error {
	// Limit to processing policies to 20 per second
	// TODO configurable
	lim := rate.NewLimiter(time.Second, 20)
	defer lim.Stop()

	// The rules have a revision number corresponding to the import round of the data source.
	revision := uint64(0)

	rulesChanged :=
		stream.Trigger(
			d.ctx,
			stream.Filter(d.State.Observable, state.Event.ForExtPolicyRules))

	for {
		// TODO: Doing this as one large transaction, but this
		// could also be split up into batches if necessary.
		tx := d.State.Write()
		extPolicyRules := d.ExtPolicyRules.Modify(tx)
		selectorPolicies := d.SelectorPolicies.Modify(tx)

		// Get new and changed rules with revisions
		// higher than the previously processed.
		ruleIter, err := extPolicyRules.LowerBound(
			state.ByRevision(revision + 1),
		)
		if err != nil {
			// TODO: This can only fail on bad queries, e.g. missing
			// indices and stuff. We should trigger a shutdown.
			return fmt.Errorf("ExtPolicyRules().LowerBound failed: %w", err)
		}

		for r, ok := ruleIter.Next(); ok; r, ok = ruleIter.Next() {
			d.processRule(selectorPolicies, r)

			// Remember the highest seen revision for the next round.
			if r.Revision > revision {
				revision = r.Revision
			}
		}
		if err := tx.Commit(); err != nil {
			// Commit could fail if the reflector says no, e.g. due to
			// persisting of some table failing or unallowed modifications made.
			// Unclear how to handle this, e.g. should failures to persist to
			// external systems bubble up here or should those be handled by the
			// persistence layer?
			return fmt.Errorf("state commit failed: %w", err)
		}

		select {
		case <-d.ctx.Done():
			return nil
		case <-rulesChanged:
			// New changes, wait a bit before the next round.
			lim.Wait(d.ctx)

		}
	}
}

func (d *distillery) selectorPolicyLoop() error {
	// TODO configurable
	lim := rate.NewLimiter(time.Second, 20)
	defer lim.Stop()

	// The rules have a revision number corresponding to the import round of the data source.
	revision := uint64(0)

	selectorPolicyChanged :=
		stream.Trigger(
			d.ctx,
			stream.Filter(d.State.Observable, state.Event.ForSelectorPolicies))

	for {
		// TODO: Doing this as one large transaction, but this
		// could also be split up into batches if necessary.
		tx := d.State.Write()
		extPolicyRules := d.ExtPolicyRules.Read(tx)
		selectorPolicies := d.SelectorPolicies.Modify(tx)

		// TODO: we likely want to do a Get of all new selector policies
		// e.g. ones that don't have a computed L4Policy!
		spIter, err := selectorPolicies.LowerBound(
			state.ByRevision(revision + 1),
		)
		if err != nil {
			// TODO: This can only fail on bad queries, e.g. missing
			// indices and stuff. We should trigger a shutdown.
			return fmt.Errorf("SelectorPolicies().LowerBound failed: %w", err)
		}

		for sp, ok := spIter.Next(); ok; sp, ok = spIter.Next() {
			d.processSelectorPolicy(extPolicyRules, selectorPolicies, sp)

			// Remember the highest seen revision for the next round.
			if sp.Revision > revision {
				revision = sp.Revision
			}
		}
		if err := tx.Commit(); err != nil {
			// Commit could fail if the reflector says no, e.g. due to
			// persisting of some table failing or unallowed modifications made.
			// Unclear how to handle this, e.g. should failures to persist to
			// external systems bubble up here or should those be handled by the
			// persistence layer?
			return fmt.Errorf("state commit failed: %w", err)
		}

		select {
		case <-d.ctx.Done():
			return nil
		case <-selectorPolicyChanged:
			// New changes, wait a bit before the next round.
			lim.Wait(d.ctx)

		}
	}
}

func (d *distillery) processSelectorPolicy(extPolicyRules state.TableReader[*structs.ExtPolicyRule], selectorPolicies state.TableReaderWriter[*structs.SelectorPolicy], sp *structs.SelectorPolicy) error {
	if len(sp.L4Policy.SourceRules) > 0 {
		// Already has associated rules, so this will be processed by incremental
		// updates.
		// TODO explicitly mark SelectorPolicy's as new and query on those.
		return nil
	}

	ruleIter, err := extPolicyRules.Get(state.All)
	if err != nil {
		return err
	}
	return state.ProcessEach(
		ruleIter,
		func(r *structs.ExtPolicyRule) error {
			if r.EndpointSelector.Matches(sp.Labels) {
				return d.updateSelector(selectorPolicies, sp, r)
			}
			return nil
		})
}

func (d *distillery) processRule(selectorPolicies state.TableReaderWriter[*structs.SelectorPolicy], r *structs.ExtPolicyRule) error {
	if r.Gone {
		// TODO find all L4Policy's that reference this rule and recompute them.
		// Then delete this rule
		panic("TBD")
	}

	// Find all selector policies that match up with the rule and then
	// recompute the L4Policy (update SourceRules and then redo the filters with
	// the new SourceRules set).
	spIter, err := selectorPolicies.Get(state.All)
	if err != nil {
		return err
	}
	return state.ProcessEach(
		spIter,
		func(sp *structs.SelectorPolicy) error {
			if r.EndpointSelector.Matches(sp.Labels) {
				return d.updateSelector(selectorPolicies, sp, r)
			}
			return nil
		})
}

func (d *distillery) updateSelector(selectorPolicies state.TableReaderWriter[*structs.SelectorPolicy], sp *structs.SelectorPolicy, changedRule *structs.ExtPolicyRule) error {
	sp = sp.DeepCopy()
	sp.Revision = sp.Revision + 1

	// Going with a naive, but simple solution here. We update the SourceRules
	// set and then recompute the filters from scratch.
	if changedRule.Gone {
		sp.L4Policy.RemoveSourceRule(changedRule.ID)
	} else {
		sp.L4Policy.AddSourceRule(changedRule.ID)
	}

	// TODO compute how these rules specifically apply to this SelectorPolicy.
	return selectorPolicies.Insert(sp)
}
