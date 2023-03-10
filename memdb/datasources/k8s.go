package datasources

import (
	"context"
	"net/netip"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"

	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	cilium_api_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/resource"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/policy/api"
)

var k8sCell = cell.Module(
	"datasource-k8s",
	"Data source for Kubernetes",

	cell.Provide(k8sResources),
	cell.Invoke(registerK8s),
)

type resourcesOut struct {
	cell.Out

	NetworkPolicies resource.Resource[*cilium_api_v2.CiliumNetworkPolicy]
	Pods            resource.Resource[*corev1.Pod]
}

func k8sResources(lc hive.Lifecycle, cs client.Clientset) (out resourcesOut) {
	if !cs.IsEnabled() {
		return
	}
	out.NetworkPolicies = resource.New[*cilium_api_v2.CiliumNetworkPolicy](
		lc,
		utils.ListerWatcherFromTyped[*cilium_api_v2.CiliumNetworkPolicyList](
			cs.CiliumV2().CiliumNetworkPolicies(""),
		))

	out.Pods = resource.New[*corev1.Pod](
		lc,
		utils.ListerWatcherFromTyped[*corev1.PodList](
			cs.CoreV1().Pods(""),
		))

	return
}

type k8sParams struct {
	cell.In

	Lifecycle       hive.Lifecycle
	NetworkPolicies resource.Resource[*cilium_api_v2.CiliumNetworkPolicy]
	Pods            resource.Resource[*corev1.Pod]

	Log   logrus.FieldLogger
	State *state.State
}

func registerK8s(p k8sParams) {
	stop := make(chan struct{})
	p.Lifecycle.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			go k8sDataSourceLoop(p, stop)
			return nil
		},
		OnStop: func(hive.HookContext) error {
			close(stop)
			return nil
		},
	})
}

func k8sDataSourceLoop(p k8sParams, stop chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The revision of the "import round".
	// TODO could we just have revision of the write transaction? e.g.
	// revision of the state or do we want to assign some semantic meaning to
	// the revisions in each table?
	revision := uint64(0)
	cnps := p.NetworkPolicies.Events(ctx)
	pods := p.Pods.Events(ctx)

	for {
		revision = revision + 1

		select {
		case <-stop:
			return

		case ev := <-pods:
			// Create an Endpoint from the Pod so we can play
			// with matching up identities and policies to it.
			tx := p.State.WriteTx()

			switch ev.Kind {
			case resource.Sync:
			case resource.Upsert:
				pod := ev.Object
				opLabels := labels.NewOpLabels()
				opLabels.ModifyIdentityLabels(
					labels.Map2Labels(pod.ObjectMeta.Labels, labels.LabelSourceK8s),
					nil)
				ep := &state.Endpoint{
					ID:               state.NewUUID(), // TODO can we use k8s assigned?
					Name:             pod.Name,
					Namespace:        pod.Namespace,
					CreatedAt:        pod.CreationTimestamp.Time,
					Revision:         1,
					ContainerID:      "fake",
					IPv4:             netip.Addr{},
					IPv6:             netip.Addr{},
					Labels:           opLabels,
					LabelKey:         state.LabelKey(opLabels.IdentityLabels().SortedList()),
					State:            state.EPInit,
					SelectorPolicyID: "", // TODO allocation for identity.
				}

				err := tx.Endpoints().Insert(ep)
				if err != nil {
					p.Log.WithError(err).Error("Endpoint creation failed")
				} else {
					p.Log.WithField("name", pod.Name).Info("Endpoint created")
				}

			case resource.Delete:
				pod := ev.Object
				ep, _ := tx.Endpoints().First(state.ByName(pod.Namespace, pod.Name))
				if ep != nil {
					tx.Endpoints().Delete(ep)
					p.Log.WithField("name", pod.Name).Info("Endpoint deleted")
				}
			}
			if err := tx.Commit(); err != nil {
				panic(err)
			}
			ev.Done(nil)

		case ev := <-cnps:

			// FIXME batching?
			tx := p.State.WriteTx()
			policies := tx.ExtNetworkPolicies()
			rules := tx.ExtPolicyRules()

			var txError error
			cnp := ev.Object
			enp, _ := policies.First(state.ByName(ev.Key.Namespace, ev.Key.Name))
			if enp == nil && cnp != nil {
				enp = &state.ExtNetworkPolicy{
					ExtMeta: state.ExtMeta{
						ID:        state.NewUUID(),
						Name:      cnp.Name,
						Namespace: cnp.Namespace,
						Revision:  revision,
						Labels:    cnp.Labels,
					},
				}
			} else if ev.Kind != resource.Delete {
				enp = enp.Copy()
			}

			convertRule := func(r *api.Rule) *state.ExtPolicyRule {
				epr := &state.ExtPolicyRule{
					// Rules inherit the metadata of the creating policy.
					ExtMeta: enp.ExtMeta,
					Rule:    r,
				}
				epr.ID = state.NewUUID()
				return epr
			}

			switch ev.Kind {
			case resource.Sync:
			case resource.Upsert:
				cnp := ev.Object
				enp.Revision = revision
				enp.Labels = cnp.Labels

				// Mark all previous rules created by this network policy as gone.
				// They will be deleted from the table when they have been processed.
				oldRulesIter, err := rules.Get(state.ByName(enp.Namespace, enp.Name))
				if err != nil {
					txError = err
					break
				}
				for oldRule, ok := oldRulesIter.Next(); ok; oldRule, ok = oldRulesIter.Next() {
					if oldRule.Gone {
						continue
					}
					oldRuleCopy := *oldRule
					oldRuleCopy.Gone = true
					txError = rules.Insert(&oldRuleCopy)
					if txError != nil {
						break
					}
				}
				if txError != nil {
					break
				}

				newRules := mapSlice(cnp.Specs, convertRule)
				if cnp.Spec != nil {
					newRules = append(newRules, convertRule(cnp.Spec))
				}

				txError = policies.Insert(enp)
				if txError != nil {
					break
				}

				for _, r := range newRules {
					if txError != nil {
						break
					}
					txError = rules.Insert(r)
				}

			case resource.Delete:
				txError = policies.Delete(enp)
				if txError == nil {
					_, txError = rules.DeleteAll(state.ByName(enp.Namespace, enp.Name))
				}
				// TODO delete the rules
			}
			ev.Done(txError)
			if txError == nil {
				tx.Commit()
			} else {
				tx.Abort()
			}
		}
	}
}

func mapSlice[A, B any](in []A, fn func(A) B) []B {
	out := make([]B, len(in))
	for i := range in {
		out[i] = fn(in[i])
	}
	return out
}

func flatten[T any](xss [][]T) []T {
	var out []T
	for _, xs := range xss {
		for _, x := range xs {
			out = append(out, x)
		}
	}
	return out
}

func flatMap[A, B any](xss [][]A, fn func(A) B) []B {
	var out []B
	for _, xs := range xss {
		for _, x := range xs {
			out = append(out, fn(x))
		}
	}
	return out
}
