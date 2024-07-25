package experimental

import (
	"context"
	"log/slog"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"

	"github.com/cilium/cilium/pkg/k8s"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/loadbalancer"
)

// CECHandlerCell implements processing for CiliumEnvoyConfig in order
// to configure L7 redirection of services to the Envoy proxy.
// Sets the [Service.L7ProxyPort].
//
// This implements the part of pkg/ciliumenvoyconfig that does the
// [ServiceManager.RegisterL7LBServiceRedirect] etc. calls. Managing
// Envoy listeners etc. is not part of this.
var CECHandlerCell = cell.Module(
	"cec-handler",
	"CiliumEnvoyConfig handler",

	cell.ProvidePrivate(
		NewCECTable,
		newCECHandler,
	),
	cell.Provide(
		// Provide Table[*CiliumEnvoyConfig] for read-only access from other
		// modules.
		statedb.RWTable[*ciliumv2.CiliumEnvoyConfig].ToTable,
	),

	cell.Invoke(
		registerCECReflector,
		(*cecHandler).register,
	),
)

const (
	CECTableName = "ciliumenvoyconfigs"
)

var (
	CECNameIndex = statedb.Index[*ciliumv2.CiliumEnvoyConfig, string]{
		Name: "name",
		FromObject: func(obj *ciliumv2.CiliumEnvoyConfig) index.KeySet {
			// FIXME: Figure out which type to use for k8s object names.
			return index.NewKeySet(index.String(obj.Namespace + "/" + obj.Name))
		},
		FromKey: index.String,
		Unique:  true,
	}
	CECServiceNameIndex = statedb.Index[*ciliumv2.CiliumEnvoyConfig, string]{
		Name: "service-name",
		FromObject: func(obj *ciliumv2.CiliumEnvoyConfig) index.KeySet {
			ks := make([]index.Key, len(obj.Spec.Services))
			for i, svc := range obj.Spec.Services {
				ks[i] = index.String(svc.Namespace + "/" + svc.Name)
			}
			return index.NewKeySet(ks...)
		},
		FromKey: index.String,
		Unique:  false,
	}
)

func NewCECTable(db *statedb.DB) (statedb.RWTable[*ciliumv2.CiliumEnvoyConfig], error) {
	tbl, err := statedb.NewTable(
		CECTableName,
		CECNameIndex,
		CECServiceNameIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

func registerCECReflector(db *statedb.DB, jg job.Group, client client.Clientset, tbl statedb.RWTable[*ciliumv2.CiliumEnvoyConfig]) {
	if !client.IsEnabled() {
		return
	}

	cfg := k8s.ReflectorConfig[*ciliumv2.CiliumEnvoyConfig]{
		ListerWatcher: utils.ListerWatcherFromTyped(client.CiliumV2().CiliumEnvoyConfigs("" /* all namespaces */)),
		Table:         tbl,
	}
	k8s.RegisterReflector(jg, db, cfg)
}

type cecHandlerParams struct {
	cell.In

	Log    *slog.Logger
	DB     *statedb.DB
	CECs   statedb.Table[*ciliumv2.CiliumEnvoyConfig]
	Writer *Writer
}

type cecHandler struct {
	p cecHandlerParams
}

// onServiceUpsert is called when a service is upserted. It checks if there's an associated
// proxy port for the service and if so sets the proxy port.
func (h *cecHandler) onServiceUpsert(txn statedb.ReadTxn, svc *Service) {
	if svc.L7ProxyPort != 0 {
		// Proxy port already set. Changes to proxy port are handled in reconcile()
		return
	}

	// Look up if there is a CiliumEnvoyConfig that references this service.
	cec, _, found := h.p.CECs.Get(txn, CECServiceNameIndex.Query(svc.Name.Namespace+"/"+svc.Name.Name))
	if !found {
		return
	}

	for _, cecSVC := range cec.Spec.Services {
		if cecSVC.Namespace == svc.Name.Namespace && cecSVC.Name == svc.Name.Name {
			// TODO: We need the cecResourceParser that allocates the port etc.
			// For now, we just fake it with a dummy port number.
			// reconciler() could do the parsing of resources and allocating the port
			// and it'd be fine if this runs before that has happened since it will eventually
			// catch up and finish the processing and updates the L7ProxyPort.
			h.p.Log.Info("Setting L7ProxyPort", "ServiceName", svc.Name, "Listener", cecSVC.Listener)
			svc.L7ProxyPort = 12345
		}
	}
}

func (h *cecHandler) reconciler(ctx context.Context, health cell.Health) error {
	wtxn := h.p.DB.WriteTxn(h.p.CECs)
	changes, err := h.p.CECs.Changes(wtxn)
	wtxn.Commit()
	if err != nil {
		return err
	}
	defer changes.Close()

	services := h.p.Writer.Services()

	for {
		txn := h.p.Writer.WriteTxn()
		for change, _, ok := changes.Next(); ok; change, _, ok = changes.Next() {
			cec := change.Object

			for _, cecSVC := range cec.Spec.Services {
				name := loadbalancer.ServiceName{
					Namespace: cecSVC.Namespace,
					Name:      cecSVC.Name,
				}
				svc, _, found := services.Get(txn, ServiceByName(name))
				if found {
					svc = svc.Clone()
					if change.Deleted {
						h.p.Log.Info("Removing L7ProxyPort", "ServiceName", name, "Listener", cecSVC.Listener)
						svc.L7ProxyPort = 0
					} else {
						// TODO proper port
						h.p.Log.Info("Setting L7ProxyPort", "ServiceName", name, "Listener", cecSVC.Listener)
						svc.L7ProxyPort = 12345
					}
					h.p.Writer.UpsertService(txn, svc)
				}
			}
		}
		txn.Commit()

		select {
		case <-ctx.Done():
		case <-changes.Watch(h.p.DB.ReadTxn()):
		}

	}

}

func (h *cecHandler) register(g job.Group) {
	g.Add(job.OneShot("cec-reconciler", h.reconciler))
}

type cecHandlerOut struct {
	cell.Out

	Handler     *cecHandler
	ServiceHook ServiceHook `group:"service-hooks"`
}

func newCECHandler(g job.Group, p cecHandlerParams) cecHandlerOut {
	h := &cecHandler{p}
	hook := func(txn statedb.ReadTxn, svc *Service) {
		h.onServiceUpsert(txn, svc)
	}
	return cecHandlerOut{
		Handler:     h,
		ServiceHook: hook,
	}
}
