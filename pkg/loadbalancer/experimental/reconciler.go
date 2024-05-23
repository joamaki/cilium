// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cilium/hive/cell"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/reconciler"

	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/service"
	"github.com/cilium/cilium/pkg/time"
)

// ReconcilerCell reconciles Table[Frontend] with the ServiceManager
// as a transitional mechanism. The target end state is to replace ServiceManager with a
// more direct reconciler that reconciles the BPF maps from the Service object.
//
// This is experimental and disabled by default and can be enabled with the hidden
// "enable-new-services" flag.
var ReconcilerCell = cell.Module(
	"new-services-reconciler",
	"Reconciles service table with ServiceManager",

	cell.ProvidePrivate(
		newServicesReconcilerConfig,
	),
	cell.Invoke(
		func(cfg Config, rcfg reconciler.Config[*Service], rparams reconciler.Params) error {
			if !cfg.EnableNewServices {
				return nil
			}
			return reconciler.Register(rcfg, rparams)
		},
	),
)

func newServicesReconcilerConfig(cfg Config, s *Services, p serviceManagerOpsParams) reconciler.Config[*Service] {
	if !cfg.EnableNewServices {
		return reconciler.Config[*Service]{}
	}
	ops := &serviceManagerOps{p}
	return reconciler.Config[*Service]{
		FullReconcilationInterval: 0, // No full reconciliation for now.
		RetryBackoffMinDuration:   100 * time.Millisecond,
		RetryBackoffMaxDuration:   time.Minute,
		IncrementalRoundSize:      300,
		CloneObject:               (*Service).Clone,
		GetObjectStatus:           (*Service).getStatus,
		SetObjectStatus:           (*Service).setStatus,
		Operations:                ops,
		BatchOperations:           nil,
		Table:                     s.svcs,
	}
}

type serviceManagerOpsParams struct {
	cell.In

	Log      *slog.Logger
	Services statedb.Table[*Service]
	Backends statedb.Table[*Backend]
	Manager  service.ServiceManager
}

type serviceManagerOps struct {
	serviceManagerOpsParams
}

// Delete implements reconciler.Operations.
func (ops *serviceManagerOps) Delete(ctx context.Context, txn statedb.ReadTxn, svc *Service) error {
	var errs []error
	iter := svc.Frontends.All()
	for _, fe, ok := iter.Next(); ok; _, fe, ok = iter.Next() {
		_, err := ops.Manager.DeleteService(fe.Address)
		if err != nil {
			errs = append(errs, err)
		}
		ops.Log.Debug("Deleting service", "address", fe.Address, "error", err)
	}
	return errors.Join(errs...)
}

// Prune implements reconciler.Operations.
func (ops *serviceManagerOps) Prune(context.Context, statedb.ReadTxn, statedb.Iterator[*Service]) error {
	ops.Log.Debug("Prune (no-op)")
	// We cannot implement Prune() since this is an additional interface towards ServiceManager and thus not
	// the only source. ServiceManager itself does the pruning when SyncWithK8sFinished() is invoked.
	return nil
}

// Update implements reconciler.Operations.
func (ops *serviceManagerOps) Update(ctx context.Context, txn statedb.ReadTxn, svc *Service) error {
	svc, _, ok := ops.Services.Get(txn, ServiceNameIndex.Query(svc.Name))
	if !ok {
		return fmt.Errorf("TODO how to handle missing service?")
	}

	// Collect all backends for the service with matching protocols (L3&L4) and convert
	// to the SVC type.
	iter := GetBackendsForService(txn, ops.Backends, svc)

	iterFe := svc.Frontends.All()
	for _, fe, ok := iterFe.Next(); ok; _, fe, ok = iterFe.Next() {
		bes := statedb.Collect(
			statedb.Filter(iter, func(be *Backend) bool {
				return fe.Address.ProtoEqual(&be.L3n4Addr)
			}))
		_, id, err := ops.Manager.UpsertService(toSVC(fe.Address, svc, &fe, bes))
		ops.Log.Debug("Updated service", "address", fe.Address, "frontend-id", id, "type", fe.Type, "backends", bes, "error", err)
		if err != nil {
			return err
		}
		fe.ID = id

		svc.Frontends = svc.Frontends.Set(fe.Address, fe)
	}

	return nil
}

var _ reconciler.Operations[*Service] = &serviceManagerOps{}

func toBackends(bes []*Backend) []*loadbalancer.Backend {
	out := make([]*loadbalancer.Backend, len(bes))
	for i := range bes {
		out[i] = &bes[i].Backend
		out[i].State = bes[i].ActualState
	}
	return out
}

func toSVC(addr loadbalancer.L3n4Addr, svc *Service, fe *Frontend, bes []*Backend) *loadbalancer.SVC {
	return &loadbalancer.SVC{
		Frontend:                  loadbalancer.L3n4AddrID{L3n4Addr: addr},
		Backends:                  toBackends(bes),
		Type:                      fe.Type,
		ExtTrafficPolicy:          svc.ExtTrafficPolicy,
		IntTrafficPolicy:          svc.IntTrafficPolicy,
		NatPolicy:                 svc.NatPolicy,
		SessionAffinity:           svc.SessionAffinity,
		SessionAffinityTimeoutSec: svc.SessionAffinityTimeoutSec,
		HealthCheckNodePort:       svc.HealthCheckNodePort,
		Name:                      svc.Name,
		LoadBalancerSourceRanges:  svc.LoadBalancerSourceRanges,
		L7LBProxyPort:             svc.L7ProxyPort,
		LoopbackHostport:          svc.LoopbackHostport,
	}
}
