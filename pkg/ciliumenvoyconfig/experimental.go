// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package ciliumenvoyconfig

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"maps"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	envoy_config_core "github.com/cilium/proxy/go/envoy/config/core/v3"
	envoy_config_endpoint "github.com/cilium/proxy/go/envoy/config/endpoint/v3"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/part"
	"github.com/cilium/statedb/reconciler"
	"github.com/cilium/stream"
	k8sTypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/cilium/cilium/pkg/ciliumenvoyconfig/types"
	"github.com/cilium/cilium/pkg/envoy"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/labels"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/policy"
	"github.com/cilium/cilium/pkg/policy/api"
	"github.com/cilium/cilium/pkg/rate"
	"github.com/cilium/cilium/pkg/time"
)

var (
	experimentalCell = cell.Module(
		"experimental",
		"Integration to experimental LB control-plane",

		cell.ProvidePrivate(
			cecListerWatchers,
			newPolicyTrigger,
			func(xds envoy.XDSServer) resourceMutator { return xds },
		),

		experimentalTableCells,
		experimentalControllerCells,
	)

	experimentalControllerCells = cell.Group(
		cell.Module(
			"controller",
			"CiliumEnvoyConfig controller",
			cell.Provide(
				newCECController,
			),
			cell.Invoke((*cecController).setWriter),
		),
		cell.Module(
			"reconciler",
			"Reconciles resources with Envoy",
			cell.Invoke(registerEnvoyReconciler),
		),
	)

	experimentalTableCells = cell.Group(
		cell.ProvidePrivate(
			types.NewCECTable,
			statedb.RWTable[*types.CEC].ToTable,
			newNodeLabels,
		),
		cell.Module("reflector",
			"Reflects CiliumEnvoyConfig to table",
			cell.Invoke(registerCECReflector),
		),
	)
)

type cecControllerParams struct {
	cell.In

	DB             *statedb.DB
	JobGroup       job.Group
	Log            *slog.Logger
	ExpConfig      experimental.Config
	LocalNodeStore *node.LocalNodeStore

	NodeLabels    *nodeLabels
	CECs          statedb.RWTable[*types.CEC]
	EnvoySyncer   resourceMutator
	PolicyTrigger policyTrigger
}

type resourceMutator interface {
	DeleteEnvoyResources(context.Context, envoy.Resources) error
	UpdateEnvoyResources(context.Context, envoy.Resources, envoy.Resources) error
	UpsertEnvoyResources(context.Context, envoy.Resources) error
}

type policyTrigger interface {
	TriggerPolicyUpdates()
}

type cecController struct {
	params cecControllerParams
	writer *experimental.Writer
}

type cecControllerOut struct {
	cell.Out

	C    *cecController
	Hook experimental.ServiceHook `group:"service-hooks"`
}

func newNodeLabels() *nodeLabels {
	nl := &nodeLabels{}
	lbls := map[string]string{}
	nl.Store(&lbls)
	return nl
}

func newCECController(params cecControllerParams) cecControllerOut {
	if !params.ExpConfig.EnableExperimentalLB {
		return cecControllerOut{}
	}

	c := &cecController{params, nil}
	params.JobGroup.Add(job.OneShot("control-loop", c.loop))
	return cecControllerOut{
		C:    c,
		Hook: c.onServiceUpsert,
	}
}

func (c *cecController) setWriter(w *experimental.Writer) {
	if c != nil {
		c.writer = w
	}
}

// loop watches CECs, services and backends to:
// - set L7 proxy redirects on services
// - synchronize backends of matched services to Envoy
//
// The creation of listeners happens via the reconciler using [envoyOps].
func (c *cecController) loop(ctx context.Context, health cell.Health) error {
	var (
		// Services table is used for looking up the service on which we're
		// setting the proxy redirection.
		svcs statedb.Table[*experimental.Service] = c.writer.Services()

		// Iterator for changes to services.
		servicesIter statedb.ChangeIterator[*experimental.Service]

		// Iterator for changes (upsert/delete) to the cilium envoy config table.
		// We process each change and look up a referenced service to set/unset the
		// proxy redirection.
		cecs statedb.ChangeIterator[*types.CEC]

		// Iterator for changes to backendsIter.
		backendsIter statedb.ChangeIterator[*experimental.Backend]

		// cecToServices maps from CEC to the set of services it references.
		cecToServices = map[k8sTypes.NamespacedName]part.Set[loadbalancer.ServiceName]{}

		// servicesToSync is the set of service names for which we sync backends to Envoy.
		servicesToSync = map[loadbalancer.ServiceName]part.Set[k8sTypes.NamespacedName]{}

		// serviceSyncSet is the set of service names we're currently trying to sync.
		// Subset of [servicesToSync].
		serviceSyncSet = sets.New[loadbalancer.ServiceName]()

		// allocatedPorts is the set of listener ports that have been allocated.
		// Used to gauge for the need for a policy recomputation for the case
		// when the CEC is processed late.
		allocatedPorts = part.Set[uint16]{}

		// Rate limiter to process changes in larger batches and to avoid processing
		// intermediate object states.
		limiter = rate.NewLimiter(50*time.Millisecond, 1)

		// localNodeChanges is used to grab the local node labels and on changes
		// recompute the CEC node selection.
		localNodeChanges = stream.ToChannel(ctx, c.params.LocalNodeStore)

		// backendSyncTicker is a periodic ticker to retry synchronizing backends
		// on failures.
		backendSyncTicker = time.NewTicker(time.Second)
	)
	defer backendSyncTicker.Stop()

	{
		wtxn := c.params.DB.WriteTxn(c.params.CECs, c.writer.Backends(), c.writer.Services())
		var err error
		cecs, err = c.params.CECs.Changes(wtxn)
		if err == nil {
			backendsIter, err = c.writer.Backends().Changes(wtxn)
		}
		if err == nil {
			servicesIter, err = c.writer.Services().Changes(wtxn)
		}
		wtxn.Commit()
		if err != nil {
			return err
		}

		initialNode := <-localNodeChanges
		c.params.NodeLabels.Store(&initialNode.Labels)
	}

	for {
		// Hold onto the previously allocated listener ports. If the ports change we trigger
		// policy recomputation.
		prevAllocatedPorts := allocatedPorts

		// Process the changed CECs and set the proxy ports of referenced services.
		// With the WriteTxn held we should do nothing more than update services and
		// collect what we need to sync towards Envoy.
		wtxn := c.writer.WriteTxn()
		cecChanges, cecWatch := cecs.Next(wtxn)
		for change := range cecChanges {
			cec := change.Object
			if cec.Status.Kind != reconciler.StatusKindDone {
				// Only process the CEC once it has been reconciled towards Envoy.
				continue
			}

			var serviceListenerRefs part.Set[loadbalancer.ServiceName]

			// Services that need to be redirected to Envoy and which need backend syncing.
			if !change.Deleted {
				for _, svcl := range cec.Spec.Services {
					serviceListenerRefs = serviceListenerRefs.Set(svcl.ServiceName())
				}
			}

			oldRefs := cecToServices[cec.Name]
			newRefs := serviceListenerRefs.Difference(oldRefs)
			orphanRefs := oldRefs.Difference(newRefs)

			// Update L7 redirection for new and orphaned services.
			for name := range newRefs.Union(orphanRefs).All() {
				svc, _, found := svcs.Get(wtxn, experimental.ServiceByName(name))
				if found {
					// Do an upsert to call into onServiceUpsert() to fill in the L7ProxyPort.
					c.writer.UpsertService(wtxn, svc.Clone())
				}
			}

			if change.Deleted {
				delete(cecToServices, cec.Name)
			} else {
				cecToServices[cec.Name] = serviceListenerRefs
			}

			// Collect the services that don't need redirection and only backend syncing
			serviceRefs := serviceListenerRefs
			for _, beSvc := range cec.Spec.BackendServices {
				// FIXME: beSvc.Ports.
				if !change.Deleted {
					serviceRefs = serviceRefs.Set(beSvc.ServiceName())
				}
			}

			// Remove orphaned services from sync set.
			for svc := range oldRefs.Difference(serviceRefs).All() {
				if cecRefs := servicesToSync[svc].Delete(cec.Name); cecRefs.Len() > 0 {
					servicesToSync[svc] = cecRefs
				} else {
					delete(servicesToSync, svc)
					serviceSyncSet.Delete(svc)
				}
			}

			// Add the new services to sync set.
			for svc := range serviceRefs.Difference(oldRefs).All() {
				old := servicesToSync[svc]
				if new := old.Set(cec.Name); !new.Equal(old) {
					servicesToSync[svc] = new
					serviceSyncSet.Insert(svc)
				}
			}

			resources := cec.Resources.(*envoy.Resources)
			for _, l := range resources.Listeners {
				if addr := l.GetAddress(); addr != nil {
					if sa := addr.GetSocketAddress(); sa != nil {
						proxyPort := uint16(sa.GetPortValue())
						if change.Deleted {
							allocatedPorts = allocatedPorts.Delete(proxyPort)
						} else {
							allocatedPorts = allocatedPorts.Set(proxyPort)
						}
					}
				}
			}
		}

		// Commit the changes we just made to Table[Service].
		rtxn := wtxn.Commit()

		// Iterate over the changed services and backends
		// to figure out what to sync.
		serviceChanges, serviceWatch := servicesIter.Next(rtxn)
		for change := range serviceChanges {
			if _, found := servicesToSync[change.Object.Name]; found {
				serviceSyncSet.Insert(change.Object.Name)
			}
		}
		backendChanges, backendWatch := backendsIter.Next(rtxn)
		for change := range backendChanges {
			for name := range change.Object.Instances.All() {
				if _, found := servicesToSync[name]; found {
					serviceSyncSet.Insert(name)
				}
			}
		}

		if serviceSyncSet.Len() > 0 {
			c.syncBackendsToEnvoy(ctx, rtxn, serviceSyncSet)
		}

		// Retrigger policy computation if the allocated proxy ports have changed.
		// This is needed when the CEC is processed after the policies have been
		// computed.
		if !allocatedPorts.Equal(prevAllocatedPorts) {
			c.params.PolicyTrigger.TriggerPolicyUpdates()
		}

		// Rate limit to do larger transactions and to avoid processing intermediate
		// states.
		if err := limiter.Wait(ctx); err != nil {
			return err
		}

		// Wait for new changes
	wait:
		select {
		case <-ctx.Done():
			return nil
		case <-cecWatch:
		case <-backendWatch:
		case <-serviceWatch:
		case <-backendSyncTicker.C:
			// Periodic retry of failed backend syncs.
			// TODO: Alternatively we could just have a table of ServiceName->[]Backend and use the
			// statedb reconciler.
			if serviceSyncSet.Len() > 0 {
				c.syncBackendsToEnvoy(ctx, c.params.DB.ReadTxn(), serviceSyncSet)
			}
			// Go back to waiting.
			goto wait
		case localNode := <-localNodeChanges:
			newLabels := localNode.Labels
			oldLabels := *c.params.NodeLabels.Load()

			if !maps.Equal(newLabels, oldLabels) {
				c.params.Log.Debug("Labels changed", "old", oldLabels, "new", newLabels)

				// Store the new labels so the reflector can compute 'SelectsLocalNode'
				// on the fly. The reflector may already update 'SelectsLocalNode' to the
				// correct value, so the recomputation that follows may be duplicate for
				// some CECs, but that's fine.
				c.params.NodeLabels.Store(&newLabels)

				// Since the labels changed, recompute 'SelectsLocalNode'
				// for all CECs.
				ls := labels.Set(newLabels)
				wtxn := c.params.DB.WriteTxn(c.params.CECs)
				for cec := range c.params.CECs.All(wtxn) {
					if cec.Selector != nil {
						selects := cec.Selector.Matches(ls)
						if selects != cec.SelectsLocalNode {
							cec = cec.Clone()
							cec.SelectsLocalNode = selects
							c.params.CECs.Insert(wtxn, cec)
						}
					}
				}
				wtxn.Commit()
			}
		}
	}
}

func (c *cecController) syncBackendsToEnvoy(ctx context.Context, txn statedb.ReadTxn, syncSet sets.Set[loadbalancer.ServiceName]) {
	for name := range syncSet {
		// TODO: Keep track of the previous set to avoid unnecessary work changes.
		bes := statedb.ToSeq(c.writer.Backends().List(txn, experimental.BackendByServiceName(name)))
		res := envoy.Resources{
			Endpoints: backendsToLoadAssignments(name, bes),
		}
		err := c.params.EnvoySyncer.UpsertEnvoyResources(ctx, res)
		if err == nil {
			syncSet.Delete(name)
		} else {
			c.params.Log.Warn("Failed to sync backends, retrying later",
				logfields.ServiceName, name, logfields.Error, err)
		}
	}
}

func lookupProxyPort(cec *types.CEC, svcl *ciliumv2.ServiceListener) uint16 {
	if svcl.Listener != "" {
		// Listener names are qualified after parsing, so qualify the listener reference as well for it to match
		svcListener, _ := api.ResourceQualifiedName(
			cec.Name.Namespace, cec.Name.Name, svcl.Listener, api.ForceNamespace)
		port, _ := cec.Listeners.Get(svcListener)
		return port
	}
	for _, port := range cec.Listeners.All() {
		return port
	}
	return 0
}

// onServiceUpsert is called when the service is upserted, but before it is committed.
// We set the proxy port on the service if there's a matching CEC.
func (c *cecController) onServiceUpsert(txn statedb.ReadTxn, svc *experimental.Service) {
	// Look up if there is a CiliumEnvoyConfig that references this service.
	cec, _, found := c.params.CECs.Get(txn, types.CECByServiceName(svc.Name))
	if !found {
		c.params.Log.Debug("onServiceUpsert: CEC not found", "name", svc.Name)
		svc.ProxyRedirect = nil
		return
	}

	// Find the service listener that referenced this service.
	var svcl *ciliumv2.ServiceListener
	for _, l := range cec.Spec.Services {
		if l.Namespace == svc.Name.Namespace && l.Name == svc.Name.Name {
			svcl = l
			break
		}
	}
	if svcl == nil {
		panic("BUG: Table index pointed to a CEC for a service listener, but it was not there.")
	}

	proxyPort := lookupProxyPort(cec, svcl)
	c.params.Log.Debug("Setting proxy redirection (on service upsert)",
		"namespace", svcl.Namespace,
		"name", svcl.Name,
		"proxyPort", proxyPort,
		"listener", svcl.Listener)
	if proxyPort == 0 {
		svc.ProxyRedirect = nil
	} else {
		svc.ProxyRedirect = &experimental.ProxyRedirect{
			ProxyPort: proxyPort,
			Ports:     svcl.Ports,
		}
	}
}

func backendsToLoadAssignments(serviceName loadbalancer.ServiceName, backends iter.Seq[*experimental.Backend]) []*envoy_config_endpoint.ClusterLoadAssignment {
	var endpoints []*envoy_config_endpoint.ClusterLoadAssignment

	// Partition backends by port name.
	backendMap := map[string][]*experimental.Backend{}
	backendMap[anyPort] = nil
	for be := range backends {
		_, found := be.Instances.Get(serviceName)
		if !found {
			continue
		}
		// FIXME: support filtering by port name if [cilium_v2.Service.Ports] is a
		// port name not number.
		//backendMap[inst.PortName] = append(backendMap[inst.PortName], be)

		backendMap[anyPort] = append(backendMap[anyPort], be)
	}

	for port, bes := range backendMap {
		var lbEndpoints []*envoy_config_endpoint.LbEndpoint
		for _, be := range bes {
			if be.Protocol != loadbalancer.TCP {
				// Only TCP services supported with Envoy for now
				continue
			}

			lbEndpoints = append(lbEndpoints, &envoy_config_endpoint.LbEndpoint{
				HostIdentifier: &envoy_config_endpoint.LbEndpoint_Endpoint{
					Endpoint: &envoy_config_endpoint.Endpoint{
						Address: &envoy_config_core.Address{
							Address: &envoy_config_core.Address_SocketAddress{
								SocketAddress: &envoy_config_core.SocketAddress{
									Address: be.AddrCluster.String(),
									PortSpecifier: &envoy_config_core.SocketAddress_PortValue{
										PortValue: uint32(be.Port),
									},
								},
							},
						},
					},
				},
			})
		}

		endpoint := &envoy_config_endpoint.ClusterLoadAssignment{
			ClusterName: fmt.Sprintf("%s:%s", serviceName.String(), port),
			Endpoints: []*envoy_config_endpoint.LocalityLbEndpoints{
				{
					LbEndpoints: lbEndpoints,
				},
			},
		}
		endpoints = append(endpoints, endpoint)

		// for backward compatibility, if any port is allowed, publish one more
		// endpoint having cluster name as service name.
		if port == anyPort {
			endpoints = append(endpoints, &envoy_config_endpoint.ClusterLoadAssignment{
				ClusterName: serviceName.String(),
				Endpoints: []*envoy_config_endpoint.LocalityLbEndpoints{
					{
						LbEndpoints: lbEndpoints,
					},
				},
			})
		}
	}

	return endpoints
}

type policyTriggerWrapper struct{ updater *policy.Updater }

func (p policyTriggerWrapper) TriggerPolicyUpdates() {
	p.updater.TriggerPolicyUpdates(true, "Envoy Listeners changed")
}

func newPolicyTrigger(log *slog.Logger, updater *policy.Updater) policyTrigger {
	return policyTriggerWrapper{updater}
}

type envoyOps struct {
	log *slog.Logger
	xds resourceMutator
}

// Delete implements reconciler.Operations.
func (ops *envoyOps) Delete(ctx context.Context, _ statedb.ReadTxn, cec *types.CEC) error {
	if prev := cec.ReconciledResources.(*envoy.Resources); prev != nil {
		// Perform the deletion with the resources that were last successfully reconciled
		// instead of whatever the latest one is (which would have not been pushed to Envoy).
		return ops.xds.DeleteEnvoyResources(ctx, *prev)
	}
	return nil
}

// Prune implements reconciler.Operations.
func (ops *envoyOps) Prune(ctx context.Context, txn statedb.ReadTxn, objects iter.Seq2[*types.CEC, statedb.Revision]) error {
	return nil
}

// Update implements reconciler.Operations.
func (ops *envoyOps) Update(ctx context.Context, txn statedb.ReadTxn, cec *types.CEC) error {
	var prevResources envoy.Resources
	if cec.ReconciledResources != nil {
		prevResources = *cec.ReconciledResources.(*envoy.Resources)
	}

	var err error
	if cec.SelectsLocalNode {
		resources := cec.Resources.(*envoy.Resources)
		err := ops.xds.UpdateEnvoyResources(ctx, prevResources, *resources)
		if err == nil {
			cec.ReconciledResources = resources
		}
	} else if cec.ReconciledResources != nil {
		// The local node no longer selected and it had been reconciled to envoy previously.
		// Delete the resources and forget.
		err = ops.xds.DeleteEnvoyResources(ctx, prevResources)
		if err == nil {
			cec.ReconciledResources = nil
		}
	}
	return err
}

var _ reconciler.Operations[*types.CEC] = &envoyOps{}

func registerEnvoyReconciler(log *slog.Logger, xds resourceMutator, params reconciler.Params, cecs statedb.RWTable[*types.CEC]) error {
	ops := &envoyOps{
		log: log, xds: xds,
	}
	_, err := reconciler.Register(
		params,
		cecs,
		(*types.CEC).Clone,
		(*types.CEC).SetStatus,
		(*types.CEC).GetStatus,
		ops,
		nil,
		reconciler.WithoutPruning(),
	)
	return err
}

func init() {
	part.RegisterKeyType(
		func(nn k8sTypes.NamespacedName) []byte {
			return []byte(nn.String())
		},
	)
}
