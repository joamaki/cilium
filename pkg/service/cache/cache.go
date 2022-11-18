package cache

import (
	"context"
	"net"
	"sync"

	"github.com/cilium/cilium/pkg/clustermesh"
	"github.com/cilium/cilium/pkg/hive"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/service/store"
	"github.com/cilium/workerpool"
	"github.com/davecgh/go-spew/spew"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"

	datapathTypes "github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/k8s/resource"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/stream"
)

// Temporary type aliases until the data types have been migrated
// somewhere sane.
type (
	Service        = k8s.Service
	ServiceID      = k8s.ServiceID
	Endpoints      = k8s.Endpoints
	EndpointSlices = k8s.EndpointSlices
)

// Services provides lookups of services the associated endpoints and
// an event stream for changes.
//
// The lookup methods will block until the services and endpoints have been
// synchronized and all subscribers have finished processing the events.
//
// The event stream does not provide a replay of events prior to subscription.
// If full history is required, then subscribe before start. The events are emitted
// synchronously, so a subscriber can process the event synchronously to block
// lookups prior to synchronization, e.g. to make sure GetServiceIP() only
// returns post-sync results that have already been applied to datapath.
//
// The event handlers must not call any of the Get* methods as they may block
// waiting for the synchronization.
type ServiceLookup interface {
	stream.Observable[*ServiceEvent]

	EnsureService(svcID k8s.ServiceID, swg *lock.StoppableWaitGroup) bool
	GetEndpointsOfService(svcID ServiceID) *Endpoints
	GetServiceAddrsWithType(svcID k8s.ServiceID, svcType loadbalancer.SVCType) (map[loadbalancer.FEPortName][]*loadbalancer.L3n4Addr, int)
	GetServiceFrontendIP(svcID ServiceID, svcType loadbalancer.SVCType) net.IP
	GetServiceIP(svcID ServiceID) *loadbalancer.L3n4Addr
}

// TODO: This is separate only because of DebugStatus(). Consider exposing the "debug.RegisterStatusObject"
// stuff as a cell and have service cache register itself.
type ServiceCache interface {
	ServiceLookup

	// FIXME: clever or a sin to embed these interfaces here this way?
	// would it be better to have these in e.g. pkg/service/types?
	// Could also just repeat the methods and then assert compliance.
	store.ServiceMerger
	clustermesh.ServiceMerger

	DebugStatus() string
}

var Cell = cell.Module(
	"service-cache",
	"Service Cache stores services and associated endpoints",
	cell.Provide(newServiceCache),
)

type serviceCacheState struct {
	services map[ServiceID]*Service

	// endpoints maps a service to a map of EndpointSlices. In case the cluster
	// is still using the v1.Endpoints, the key used in the internal map of
	// EndpointSlices is the v1.Endpoint name.
	endpoints map[ServiceID]*EndpointSlices

	// selfNodeZoneLabel implements the Kubernetes topology aware hints
	// by selecting only the backends in this node's zone.
	selfNodeZoneLabel string
}

type serviceCache struct {
	serviceCacheParams

	// serviceCacheState is the mutable state associated with the service cache.
	// It is separated into its own struct for debug dumping purposes.
	serviceCacheState

	// mu protects the service cache state
	mu lock.RWMutex

	// wp is the worker pool for background workers. For now just processLoop().
	wp *workerpool.WorkerPool

	// synchronized is a wait group that is done when all subscribed resources
	// have been fully synchronized, e.g. services and endpoints map are consistent
	// and we can start serving lookups.
	synchronized sync.WaitGroup

	// TODO external endpoints

	src      stream.Observable[*ServiceEvent]
	emit     func(*ServiceEvent)
	complete func(error)
}

var _ ServiceCache = &serviceCache{}

type serviceCacheParams struct {
	cell.In

	Lifecycle hive.Lifecycle
	Log       logrus.FieldLogger
	LocalNode resource.Resource[*corev1.Node]
	Services  resource.Resource[*slim_corev1.Service]
	Endpoints resource.Resource[*Endpoints]

	// FIXME: This should not be here. It's used by k8s.ParseService() to expand
	// the nodeport frontends. That should be performed by datapath.
	NodeAddressing datapathTypes.NodeAddressing
}

func newServiceCache(p serviceCacheParams) ServiceCache {
	sc := &serviceCache{
		serviceCacheParams: p,
		serviceCacheState: serviceCacheState{
			services:  map[ServiceID]*Service{},
			endpoints: map[ServiceID]*EndpointSlices{},
		},
	}
	sc.src, sc.emit, sc.complete = stream.Multicast[*ServiceEvent]()
	p.Lifecycle.Append(sc)

	return sc
}

func (sc *serviceCache) Start(hive.HookContext) error {
	workers := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"processNodeEvents", sc.processNodeEvents},
		{"processServiceEvents", sc.processServiceEvents},
		{"processEndpointEvents", sc.processEndpointEvents},
	}
	sc.synchronized.Add(len(workers))
	sc.wp = workerpool.New(len(workers))
	for _, w := range workers {
		if err := sc.wp.Submit(w.name, w.fn); err != nil {
			return err
		}
	}
	return nil
}

func (sc *serviceCache) Stop(hive.HookContext) error {
	if sc.wp != nil {
		return sc.wp.Close()
	}
	return nil
}

func (sc *serviceCache) markSynced() error {
	sc.synchronized.Done()
	return nil
}

func (sc *serviceCache) processNodeEvents(ctx context.Context) error {
	for ev := range sc.LocalNode.Events(ctx) {
		ev.Handle(
			sc.markSynced,
			sc.updateNode,
			nil,
		)
	}
	return nil
}

func (sc *serviceCache) updateNode(key resource.Key, node *corev1.Node) error {
	sc.Log.Infof("updateNode(%s)", key)

	// FIXME move this option into "ServicesConfig" or similar. Perhaps in pkg/service/config.go?
	if !option.Config.EnableServiceTopology {
		return nil
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	labels := node.GetLabels()
	zone := labels[LabelTopologyZone]

	if sc.selfNodeZoneLabel == zone {
		return nil
	}
	sc.selfNodeZoneLabel = zone

	// Since the node label changed, we need to re-emit the topology aware services
	// as their backends may have changed due to the new labels.
	for id, svc := range sc.services {
		if !svc.TopologyAware {
			continue
		}
		if endpoints, ready := sc.correlateEndpoints(id); ready {
			sc.emit(&ServiceEvent{
				Action:     UpdateService,
				ID:         id,
				Service:    svc,
				OldService: svc,
				Endpoints:  endpoints,
			})
		}
	}
	return nil
}

func (sc *serviceCache) processServiceEvents(ctx context.Context) error {
	for ev := range sc.Services.Events(ctx) {
		ev.Handle(
			sc.markSynced,
			sc.updateService,
			sc.deleteService,
		)
	}
	return nil
}

func (sc *serviceCache) updateService(key resource.Key, k8sSvc *slim_corev1.Service) error {
	// FIXME move ParseService and the Service type into pkg/service/types or similar from
	// pkg/k8s.
	svcID, svc := k8s.ParseService(k8sSvc, sc.NodeAddressing)
	if svc == nil {
		// Retrying doesn't make sense here, since we'd just get back the
		// same object. The problem would be with our parsing code, so log
		// the error so we can fix the parsing.
		sc.Log.Errorf("Failed to parse service object %s", key)
		return nil
	}

	sc.Log.Infof("updateService: svcID=%s, svc=%#v", svcID, svc)

	sc.mu.Lock()
	defer sc.mu.Unlock()

	oldService, ok := sc.services[svcID]
	if ok {
		if oldService.DeepEqual(svc) {
			return nil
		}
	}
	sc.services[svcID] = svc

	// Check if the corresponding Endpoints resource is already available, and
	// if so emit the service event.
	endpoints, serviceReady := sc.correlateEndpoints(svcID)
	if serviceReady {
		sc.emit(&ServiceEvent{
			Action:     UpdateService,
			ID:         svcID,
			Service:    svc,
			OldService: oldService,
			Endpoints:  endpoints,
		})
	}

	return nil
}

func (sc *serviceCache) deleteService(key resource.Key, svc *slim_corev1.Service) error {
	sc.Log.Infof("deleteService(%s)", key)
	return nil
}

func (sc *serviceCache) processEndpointEvents(ctx context.Context) error {
	for ev := range sc.Endpoints.Events(ctx) {
		ev.Handle(
			sc.markSynced,
			sc.updateEndpoints,
			sc.deleteEndpoints,
		)
	}
	return nil
}
func newEndpointSlices() *EndpointSlices {
	return &EndpointSlices{
		EpSlices: map[string]*Endpoints{},
	}
}

func (sc *serviceCache) updateEndpoints(key resource.Key, newEps *Endpoints) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	esName := newEps.EndpointSliceID.EndpointSliceName
	svcID := newEps.EndpointSliceID.ServiceID

	eps, ok := sc.endpoints[svcID]
	if ok {
		if eps.EpSlices[esName].DeepEqual(newEps) {
			return nil
		}
	} else {
		eps = newEndpointSlices()
		sc.endpoints[svcID] = eps
	}

	sc.Log.Infof("updateEndpoints: added new eps: %s -> %v", esName, newEps)
	eps.Upsert(esName, newEps)

	// Check if the corresponding Endpoints resource is already available
	svc, ok := sc.services[svcID]
	endpoints, serviceReady := sc.correlateEndpoints(svcID)
	if ok && serviceReady {
		sc.Log.Infof("updateEndpoints: service is ready, emit")
		sc.emit(&ServiceEvent{
			Action:    UpdateService,
			ID:        svcID,
			Service:   svc,
			Endpoints: endpoints,
		})
	}
	return nil
}

func (sc *serviceCache) deleteEndpoints(key resource.Key, eps *Endpoints) error {
	return nil
}

// DebugStatus implements debug.StatusObject to provide debug status collection
// ability
func (sc *serviceCache) DebugStatus() string {
	sc.mu.RLock()
	str := spew.Sdump(sc.serviceCacheState)
	sc.mu.RUnlock()
	return str
}
