// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"github.com/cilium/cilium/pkg/loadbalancer"
)

func (s *Services) Wrapper(orig svcManager) svcManager {
	if !s.IsEnabled() {
		return orig
	}
	panic("TBD")
}

// svcManager is a clone of watchers.svcManager
type svcManager interface {
	DeleteService(frontend loadbalancer.L3n4Addr) (bool, error)
	GetDeepCopyServiceByFrontend(frontend loadbalancer.L3n4Addr) (*loadbalancer.SVC, bool)
	UpsertService(*loadbalancer.SVC) (bool, loadbalancer.ID, error)
}

/*

// NewServicesWrapper implements enough of ServiceManager to work as a replacement in pkg/k8s/watchers.
// This is not efficient at all since we do a single upsert/delete per transaction.
type NewServicesWrapper struct {
	db   *statedb.DB
	orig svcManager
	s    *Services
}

// DeleteService implements svcManager.
func (n *NewServicesWrapper) DeleteService(frontend loadbalancer.L3n4Addr) (bool, error) {
	wtxn := n.s.WriteTxn()
	defer wtxn.Commit()
	svc, _, found := n.s.fes.Get(wtxn, FrontendL3n4AddrIndex.Query(frontend))
	if !found {
		return false, nil
	}
	old, err := n.s.DeleteFrontend(wtxn, svc.Name, frontend)
	n.s.log.Debug("Wrapper: DeleteService", "name", svc.Name, "deleted", old != nil, "error", err)
	return old != nil, err
}

// GetDeepCopyServiceByFrontend implements svcManager.
func (n *NewServicesWrapper) GetDeepCopyServiceByFrontend(frontend loadbalancer.L3n4Addr) (*loadbalancer.SVC, bool) {
	return n.orig.GetDeepCopyServiceByFrontend(frontend)
}

// UpsertService implements svcManager.
func (n *NewServicesWrapper) UpsertService(svc *loadbalancer.SVC) (bool, loadbalancer.ID, error) {
	wtxn := n.s.WriteTxn()
	defer wtxn.Abort()

	params := FrontendParams{
		Address:                   svc.Frontend.L3n4Addr,
		Name:                      svc.Name,
		Type:                      svc.Type,
		ExtTrafficPolicy:          svc.ExtTrafficPolicy,
		IntTrafficPolicy:          svc.IntTrafficPolicy,
		NatPolicy:                 svc.NatPolicy,
		SessionAffinity:           svc.SessionAffinity,
		SessionAffinityTimeoutSec: svc.SessionAffinityTimeoutSec,
		HealthCheckNodePort:       svc.HealthCheckNodePort,
		LoadBalancerSourceRanges:  svc.LoadBalancerSourceRanges,
		L7LBProxyPort:             svc.L7LBProxyPort,
		LoopbackHostport:          svc.LoopbackHostport,
		Source:                    source.Kubernetes,
	}

	created, err := n.s.UpsertFrontendAndAllBackends(wtxn, &params, svc.Backends...)
	n.s.log.Debug("Wrapper: UpsertService", "svc", svc, "created", created, "error", err)
	if err != nil {
		return false, 0, err
	}

	wtxn.Commit()

	return created, 0, nil
}

var _ svcManager = &NewServicesWrapper{}*/
