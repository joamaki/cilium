// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"context"
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
		func(cfg Config, rcfg reconciler.Config[*Frontend], rparams reconciler.Params) error {
			if !cfg.EnableNewServices {
				return nil
			}
			return reconciler.Register(rcfg, rparams)
		},
	),
)

func newServicesReconcilerConfig(cfg Config, s *Services, p serviceManagerOpsParams) reconciler.Config[*Frontend] {
	if !cfg.EnableNewServices {
		return reconciler.Config[*Frontend]{}
	}
	ops := &serviceManagerOps{p}
	return reconciler.Config[*Frontend]{
		FullReconcilationInterval: 0, // No full reconciliation for now.
		RetryBackoffMinDuration:   100 * time.Millisecond,
		RetryBackoffMaxDuration:   time.Minute,
		IncrementalRoundSize:      300,
		CloneObject:               (*Frontend).Clone,
		GetObjectStatus:           (*Frontend).getStatus,
		SetObjectStatus:           (*Frontend).setStatus,
		Operations:                ops,
		BatchOperations:           nil,
		Table:                     s.fes,
	}
}

type serviceManagerOpsParams struct {
	cell.In

	Log      *slog.Logger
	Backends statedb.Table[*Backend]
	Manager  service.ServiceManager
}

type serviceManagerOps struct {
	serviceManagerOpsParams
}

// Delete implements reconciler.Operations.
func (ops *serviceManagerOps) Delete(ctx context.Context, txn statedb.ReadTxn, fe *Frontend) error {
	_, err := ops.Manager.DeleteService(fe.Address)
	ops.Log.Debug("Deleting service", "address", fe.Address, "error", err)
	return err
}

// Prune implements reconciler.Operations.
func (ops *serviceManagerOps) Prune(context.Context, statedb.ReadTxn, statedb.Iterator[*Frontend]) error {
	ops.Log.Debug("Prune (no-op)")
	// We cannot implement Prune() since this is an additional interface towards ServiceManager and thus not
	// the only source. ServiceManager itself does the pruning when SyncWithK8sFinished() is invoked.
	return nil
}

// Update implements reconciler.Operations.
func (ops *serviceManagerOps) Update(ctx context.Context, txn statedb.ReadTxn, fe *Frontend) error {
	// Collect all backends for the service with matching protocols (L3&L4) and convert
	// to the SVC type.
	iter := GetBackendsForFrontend(txn, ops.Backends, fe)
	bes := statedb.Collect(
		statedb.Filter(iter, func(be *Backend) bool {
			return fe.Address.ProtoEqual(&be.L3n4Addr)
		}))
	_, id, err := ops.Manager.UpsertService(toSVC(fe.Address, fe, bes))
	ops.Log.Debug("Updated service", "address", fe.Address, "frontend-id", id, "frontend", fe.FrontendParams, "backends", bes, "error", err)
	if err != nil {
		return err
	}
	fe.ID = id

	return nil
}

var _ reconciler.Operations[*Frontend] = &serviceManagerOps{}

func toBackends(bes []*Backend) []*loadbalancer.Backend {
	out := make([]*loadbalancer.Backend, len(bes))
	for i := range bes {
		out[i] = &bes[i].Backend
		out[i].State = bes[i].ActualState
	}
	return out
}

func toSVC(addr loadbalancer.L3n4Addr, svc *Frontend, bes []*Backend) *loadbalancer.SVC {
	return &loadbalancer.SVC{
		Frontend:                  loadbalancer.L3n4AddrID{L3n4Addr: addr},
		Backends:                  toBackends(bes),
		Type:                      svc.Type,
		ExtTrafficPolicy:          svc.ExtTrafficPolicy,
		IntTrafficPolicy:          svc.IntTrafficPolicy,
		NatPolicy:                 svc.NatPolicy,
		SessionAffinity:           svc.SessionAffinity,
		SessionAffinityTimeoutSec: svc.SessionAffinityTimeoutSec,
		HealthCheckNodePort:       svc.HealthCheckNodePort,
		Name:                      svc.Name,
		LoadBalancerSourceRanges:  svc.LoadBalancerSourceRanges,
		L7LBProxyPort:             svc.L7LBProxyPort,
		LoopbackHostport:          svc.LoopbackHostport,
	}
}
