package loadbalancer

import (
	"context"
	"fmt"
	"sync"

	"github.com/cilium/cilium/lbtest/datapath/api"
	"github.com/cilium/cilium/lbtest/datapath/worker"
	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/container"
	"github.com/cilium/cilium/pkg/counter"
	datapathTypes "github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	lb "github.com/cilium/cilium/pkg/loadbalancer"
)

var Cell = cell.Module(
	"datapath-loadbalancer",
	"Manages the BPF state for load-balancing",

	cell.Config(DefaultConfig),
	cell.Provide(New),

	// A datapath worker is used to retry and rate-limit load-balancing
	// requests.
	worker.NewWorkerCell("loadbalancer_worker"),
)

func New(lc hive.Lifecycle, config Config, lbmap datapathTypes.LBMap, devs api.Devices,
	worker worker.Worker) api.LoadBalancer {
	// The manager applies operations to the BPF maps and maintains
	// the relevant state. The operations it implements should be idempotent
	// as they can be retried.
	m := &lbManager{
		config:            config,
		frontends:         make(map[frontendKey]*feState),
		backendIDs:        make(map[backendKey]lb.BackendID),
		nodePortFrontends: make(map[frontendKey]nodePortFrontend),
		backendRefCount:   make(counter.Counter[backendKey]),
		frontendIDAlloc:   NewIDAllocator(1, 1000), // FIXME
		backendIDAlloc:    NewIDAllocator(1, 1000), // FIXME
		lbmap:             lbmap,
	}

	// The front exposes the public API, with each operation executed with
	// retries and rate-limiting using the worker.
	//
	// This relieves the users of the API from error handling and retrying,
	// and separates the error retrying and rate-limiting logic from the
	// manager.
	f := &lbFront{Worker: worker, m: m}

	// Start observing changes to devices in order to recompute NodePort
	// frontends.
	withLifecycledContext(lc,
		func(ctx context.Context, wg *sync.WaitGroup) {
			wg.Add(1)
			devs.Observe(ctx, f.updateDevices, func(error) { wg.Done() })
		})

	return f
}

// lbFront implement LoadBalancer. Each operation is queued to the worker
// for processing and executed by lbManager.
type lbFront struct {
	worker.Worker
	m *lbManager
}

func (f *lbFront) Upsert(fe *lb.SVC) {
	f.Enqueue(fe.Frontend.Hash(), func() error { return f.m.upsertFrontend(fe) })
}

func (f *lbFront) Delete(frontend lb.L3n4Addr) {
	key := frontend.Hash()
	f.Enqueue(key, func() error { return f.m.deleteFrontend(key) })
}

func (f *lbFront) GarbageCollect() {
	f.Enqueue("__gc__", f.m.garbageCollect)
}

func (f *lbFront) updateDevices(newDevices []api.Device) {
	f.Enqueue("__devices__", func() error { return f.m.updateDevices(newDevices) })
}

type (
	frontendKey = string
	backendKey  = string
)

// State of a single frontend. Reflects the current state in BPF maps
// and is used to compute the changes needed when applying a request.
//
// A failed request may succeed partially which is reflected in the
// state and on retry only the remaining changes are processed.
type feState struct {
	id       uint16 // FIXME overlap with "id" in FrontendID
	svc      *lb.SVC
	backends container.Set[backendKey]
	// ... ?
}

// TODO: This should probably merge with lbmap, so we'd have one
// entity that "manages" the load-balancer maps. Didn't do that
// as I didn't want to touch lbmap too much yet.
type lbManager struct {
	config Config

	nodePortFrontends map[frontendKey]nodePortFrontend

	frontends map[frontendKey]*feState // actualized state of frontends

	backendIDs      map[backendKey]lb.BackendID
	backendRefCount counter.Counter[backendKey]

	frontendIDAlloc *IDAllocator
	backendIDAlloc  *IDAllocator

	lbmap datapathTypes.LBMap

	devices []api.Device

	worker worker.Worker
}

type nodePortFrontend struct {
	svc      *lb.SVC
	expanded container.Set[frontendKey]
}

func (m *lbManager) updateDevices(devs []api.Device) error {
	m.devices = devs

	// Recompute every NodePort frontend
	svcs := make([]*lb.SVC, 0, len(m.nodePortFrontends))
	for _, np := range m.nodePortFrontends {
		svcs = append(svcs, np.svc)
	}
	for _, svc := range svcs {
		m.upsertFrontend(svc)
	}

	return nil
}

func (m *lbManager) restoreFromBPF() {
	backends, err := m.lbmap.DumpBackendMaps()
	if err != nil {
		// TODO how would we handle this error? If we retry, we should not allow other
		// requests through yet.
		panic("TODO DumpBackendMaps")
	}

	for _, be := range backends {
		_, err := m.backendIDAlloc.acquireLocalID(be.L3n4Addr, uint32(be.ID))
		if err != nil {
			panic("TODO acquireLocalID")
		}
		m.backendIDs[be.L3n4Addr.Hash()] = be.ID
	}

	frontends, errs := m.lbmap.DumpServiceMaps()
	if len(errs) != 0 {
		// TODO how would we handle this error? If we retry, we should not allow other
		// requests through yet.
		panic("TODO DumpServiceMaps")
	}

	for _, fe := range frontends {
		_, err := m.frontendIDAlloc.acquireLocalID(fe.Frontend.L3n4Addr, uint32(fe.Frontend.ID))
		if err != nil {
			panic("TODO acquireLocalID")
		}
	}
}

func (m *lbManager) garbageCollect() error {
	// TODO:
	// - assume restoreFromBPF has added to backendIDs and frontendIDs
	// - remove frontends referred to by frontendIDs that are not in m.states.
	// - remove backends that have zero refcount.
	return nil
}

func (m *lbManager) deleteFrontend(key frontendKey) error {
	if fes, ok := m.nodePortFrontends[key]; ok && len(fes.expanded) > 0 {
		for k := range fes.expanded {
			if err := m.deleteFrontendSingle(k); err != nil {
				return err
			}
		}
		delete(m.nodePortFrontends, key)
		return nil
	} else {
		return m.deleteFrontendSingle(key)
	}
}

func (m *lbManager) deleteFrontendSingle(key frontendKey) error {
	state, ok := m.frontends[key]
	if !ok {
		fmt.Printf("XXX deleteFrontend state not found for %q\n", key)
		// TODO how to handle a deletion request for missing frontend? can
		// probably only log a warning and there's no point returning an error since
		// retrying makes no difference.
		return nil
	}

	err := m.lbmap.DeleteService(
		lb.L3n4AddrID{L3n4Addr: state.svc.Frontend.L3n4Addr, ID: lb.ID(state.id)},
		len(state.backends),
		useMaglev(m.config, state.svc),
		state.svc.NatPolicy,
	)
	if err != nil {
		return err
	}

	// Clean up backends
	for beKey := range state.backends {
		if m.backendRefCount.Delete(beKey) {
			if err := m.deleteBackend(beKey); err != nil {
				return err
			}
		}
	}

	// Now that deletion completed successfully we can forget the
	// frontend state.
	delete(m.frontends, key)

	return nil
}

func (m *lbManager) allFrontendIPs() []cmtypes.AddrCluster {
	all := []cmtypes.AddrCluster{}
	for _, dev := range m.devices {
		for _, ip := range dev.IPs {
			ac, ok := cmtypes.AddrClusterFromIP(ip)
			if ok {
				all = append(all, ac)
			}
		}
	}
	return all
}

func (m *lbManager) expandNodePortFrontends(frontend *lb.SVC) []*lb.SVC {
	allIPs := m.allFrontendIPs()
	fes := make([]*lb.SVC, len(allIPs))
	for i, ip := range allIPs {
		fe := *frontend
		fe.Frontend.AddrCluster = ip
		fes[i] = &fe
	}
	return fes
}

func (m *lbManager) upsertFrontend(frontend *lb.SVC) error {
	if frontend.Type == lb.SVCTypeNodePort {
		hash := frontend.Frontend.Hash()

		keys := container.NewSet[frontendKey]()
		for _, fe := range m.expandNodePortFrontends(frontend) {
			keys.Add(fe.Frontend.Hash())
			if err := m.upsertSingle(fe); err != nil {
				// TODO: rewind? we might be leaving orphan frontends.
				// TODO: In what state do we leave things?
				return err
			}
		}

		// Delete any orphan frontends.
		if old, ok := m.nodePortFrontends[hash]; ok {
			for orphan := range old.expanded.Sub(keys) {
				if err := m.deleteFrontendSingle(orphan); err != nil {
					// TODO: In what state do we leave things?
					return err
				}
			}
		}
		m.nodePortFrontends[hash] = nodePortFrontend{svc: frontend, expanded: keys}
		return nil
	} else {
		return m.upsertSingle(frontend)
	}

}

func (m *lbManager) addBackend(key backendKey, be *lb.Backend) error {
	if _, ok := m.backendIDs[key]; ok {
		// Existing backend, nothing to do.()_
		return nil
	}
	addrId, err := m.backendIDAlloc.acquireLocalID(be.L3n4Addr, 0)
	if err != nil {
		return err
	}
	id := lb.BackendID(addrId.ID)

	// FIXME: change the LBMap types
	legacyBE := &lb.Backend{
		ID:         id,
		FEPortName: be.FEPortName,
		Weight:     be.Weight,
		NodeName:   be.NodeName,
		L3n4Addr:   be.L3n4Addr,
		State:      be.State,
		Preferred:  be.Preferred,
	}
	if err := m.lbmap.AddBackend(legacyBE, be.IsIPv6()); err != nil {
		return fmt.Errorf("adding backend %d (%s) failed: %w", id,
			be.L3n4Addr.String(), err)
	}
	m.backendIDs[key] = id
	return nil
}

func (m *lbManager) deleteBackend(key backendKey) error {
	id, ok := m.backendIDs[key]
	if !ok {
		return nil
	}

	if err := m.lbmap.DeleteBackendByID(id); err != nil {
		return fmt.Errorf("deleting backend %d failed: %w", id, err)
	}

	delete(m.backendIDs, key)
	return nil
}

func (m *lbManager) upsertSingle(frontend *lb.SVC) error {
	backends := frontend.Backends

	// This method is written to be idempotent and thus retryable. The state of the frontend is updated after each
	// successful step. On early return of an error the upsert request will be retried and this
	// method continues from where it last failed based on the state. The assumption is that
	// errors encountered here are mostly due to either low memory (spurious ENOMEM), or BPF maps being
	// full (ENOSPC) and retrying (with backoff) allows user intervention to make more space and
	// to eventually recover.

	frontendKey := frontend.Frontend.Hash()
	state, ok := m.frontends[frontendKey]
	if !ok {
		addrId, err := m.frontendIDAlloc.acquireLocalID(frontend.Frontend.L3n4Addr, 0)
		if err != nil {
			// FIXME more information to error. We probably want to classify errors
			// into few categories and provide useful hints to the operator on how
			// they can help to recover from this.
			return err
		}
		state = &feState{
			id:       uint16(addrId.ID),
			svc:      frontend,
			backends: container.NewSet[backendKey](),
		}
		m.frontends[frontendKey] = state
	}

	oldBackends := state.backends

	// Add the new backends.
	state.backends = container.NewSet[backendKey]()
	for _, backend := range backends {
		key := backend.Hash()
		state.backends.Add(key)

		if !oldBackends.Contains(key) {
			if err := m.addBackend(key, backend); err != nil {
				return err
			}
			m.backendRefCount.Add(frontendKey)
		}
		// TODO: Need to update the backend if state has changed!
	}

	// Clean up orphan backends.
	for key := range oldBackends {
		if !state.backends.Contains(key) {
			if m.backendRefCount.Delete(key) {
				m.deleteBackend(key)
			}
		}
	}

	prevBackendsCount := oldBackends.Len()

	// FIXME: We really only need the backend id and its weight, not all the data.
	// Perhaps even could update the maglev maps separately.
	legacyBackends := map[string]*lb.Backend{}
	for _, be := range backends {
		key := be.Hash()
		legacyBE := &lb.Backend{
			ID:         lb.BackendID(m.backendIDs[key]),
			FEPortName: be.FEPortName,
			Weight:     be.Weight,
			NodeName:   be.NodeName,
			L3n4Addr:   be.L3n4Addr,
			State:      be.State,
			Preferred:  be.Preferred,
		}
		legacyBackends[key] = legacyBE
	}

	// FIXME "requireNodeLocalBackends()"

	// Update the service entry
	params := datapathTypes.UpsertServiceParams{
		ID:                        state.id,
		IP:                        frontend.Frontend.AddrCluster.AsNetIP(),
		Port:                      frontend.Frontend.Port,
		ActiveBackends:            legacyBackends,
		NonActiveBackends:         nil,               // FIXME
		PreferredBackends:         legacyBackends,    // FIXME
		PrevBackendsCount:         prevBackendsCount, // Used to clean up unused slots.
		IPv6:                      frontend.Frontend.IsIPv6(),
		Type:                      frontend.Type,
		NatPolicy:                 frontend.NatPolicy,
		ExtLocal:                  false, // FIXME svcInfo.requireNodeLocalBackends
		IntLocal:                  false, // FIXME svcInfo.requireNodeLocalBackends
		Scope:                     frontend.Frontend.Scope,
		SessionAffinity:           frontend.SessionAffinity,
		SessionAffinityTimeoutSec: frontend.SessionAffinityTimeoutSec,
		CheckSourceRange:          false, // FIXME need to update and stuff, see service.go:1298
		UseMaglev:                 useMaglev(m.config, frontend),
		L7LBProxyPort:             frontend.L7LBProxyPort,
		Name:                      frontend.Name,
		LoopbackHostport:          frontend.LoopbackHostport,
	}

	if err := m.lbmap.UpsertService(&params); err != nil {
		// FIXME delete the created backends, or leave them around as we keep
		// retrying? Can consider doing a GC based on diff of backendIDs and backendRefCount.
		return err
	}

	backendAddrs := []string{}
	for _, be := range backends {
		backendAddrs = append(backendAddrs, be.L3n4Addr.String())
	}

	return nil
}

func useMaglev(config Config, fe *lb.SVC) bool {
	if config.NodePortAlg != NodePortAlgMaglev {
		return false
	}
	// Provision the Maglev LUT for ClusterIP only if ExternalClusterIP is
	// enabled because ClusterIP can also be accessed from outside with this
	// setting. We don't do it unconditionally to avoid increasing memory
	// footprint.
	if fe.Type == lb.SVCTypeClusterIP && !config.ExternalClusterIP {
		return false
	}
	// Wildcarded frontend is not exposed for external traffic.
	if fe.Type == lb.SVCTypeNodePort && isWildcardAddr(fe.Frontend.L3n4Addr) {
		return false
	}
	// Only provision the Maglev LUT for service types which are reachable
	// from outside the node.
	switch fe.Type {
	case lb.SVCTypeClusterIP,
		lb.SVCTypeNodePort,
		lb.SVCTypeLoadBalancer,
		lb.SVCTypeHostPort,
		lb.SVCTypeExternalIPs:
		return true
	}
	return false
}

var (
	wildcardIPv6 = cmtypes.MustParseAddrCluster("::")
	wildcardIPv4 = cmtypes.MustParseAddrCluster("0.0.0.0")
)

// isWildcardAddr returns true if given frontend is used for wildcard svc lookups
// (by bpf_sock).
func isWildcardAddr(frontend lb.L3n4Addr) bool {
	if frontend.IsIPv6() {
		return wildcardIPv6.Equal(frontend.AddrCluster)
	}
	return wildcardIPv4.Equal(frontend.AddrCluster)
}

func withLifecycledContext(lc hive.Lifecycle, start func(ctx context.Context, wg *sync.WaitGroup)) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	lc.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			start(ctx, &wg)
			return nil
		},
		OnStop: func(hive.HookContext) error {
			cancel()
			wg.Wait()
			return nil
		},
	})
}
