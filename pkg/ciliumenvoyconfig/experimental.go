package ciliumenvoyconfig

import (
	"context"
	"log/slog"

	"github.com/cilium/cilium/pkg/envoy"
	"github.com/cilium/cilium/pkg/k8s"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/policy/api"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

var (
	experimentalModule = cell.Module(
		"experimental",
		"Integration to experimental LB control-plane",

		cell.ProvidePrivate(cecListerWatchers),
		experimentalTableCells,
		experimentalControllerCells,
	)

	experimentalControllerCells = cell.Group(
		cell.Provide(newCECController),

		// Wire up cecController with the writer. This needs to be done
		// separately from the constructor as it's providing a ServiceHook.
		// TODO: Reconsider use of group values and instead just have Writer.AddServiceHook etc.?
		cell.Invoke((*cecController).setWriter),
	)

	experimentalTableCells = cell.Group(
		cell.ProvidePrivate(
			newCECTable,
			statedb.RWTable[*cec].ToTable,
		),
		cell.Invoke(
			registerCECReflector,
		),
	)
)

// Types for the ListerWatchers of the CEC resources. Abstracted so that tests can
// inject custom ones.
type (
	cecListerWatcher  cache.ListerWatcher
	ccecListerWatcher cache.ListerWatcher

	listerWatchers struct {
		cec  cecListerWatcher
		ccec ccecListerWatcher
	}
)

func cecListerWatchers(cs client.Clientset) (out struct {
	cell.Out
	LW listerWatchers
}) {
	if cs.IsEnabled() {
		out.LW.cec = utils.ListerWatcherFromTyped(cs.CiliumV2().CiliumEnvoyConfigs(""))
		out.LW.ccec = utils.ListerWatcherFromTyped(cs.CiliumV2().CiliumClusterwideEnvoyConfigs())
	}
	return
}

type cec struct {
	name, namespace string
	spec            *ciliumv2.CiliumEnvoyConfigSpec
	resources       envoy.Resources
}

var (
	cecNameIndex = statedb.Index[*cec, string]{
		Name: "name",
		FromObject: func(obj *cec) index.KeySet {
			return index.NewKeySet(index.String(obj.namespace + "/" + obj.name))
		},
		FromKey: index.String,
		Unique:  true,
	}

	cecByName = cecNameIndex.Query

	cecServiceIndex = statedb.Index[*cec, loadbalancer.ServiceName]{
		Name: "service",
		FromObject: func(obj *cec) index.KeySet {
			keys := make([]index.Key, len(obj.spec.Services))
			for i, svcl := range obj.spec.Services {
				keys[i] = index.String(
					loadbalancer.ServiceName{
						Namespace: svcl.Namespace,
						Name:      svcl.Name,
					}.String(),
				)
			}
			return index.NewKeySet(keys...)
		},
		FromKey: func(key loadbalancer.ServiceName) index.Key {
			return index.String(key.String())
		},
		Unique: false,
	}

	cecByServiceName = cecServiceIndex.Query
)

func newCECTable(db *statedb.DB) (statedb.RWTable[*cec], error) {
	tbl, err := statedb.NewTable(
		"ciliumenvoyconfigs",
		cecNameIndex,
		cecServiceIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

func registerCECReflector(ecfg experimental.Config, p *cecResourceParser, log *slog.Logger, lws listerWatchers, g job.Group, db *statedb.DB, tbl statedb.RWTable[*cec]) error {
	if lws.cec == nil || !ecfg.EnableExperimentalLB {
		return nil
	}
	transform := func(obj any) (*cec, bool) {
		var (
			objMeta *metav1.ObjectMeta
			spec    *ciliumv2.CiliumEnvoyConfigSpec
		)

		switch cecObj := obj.(type) {
		case *ciliumv2.CiliumEnvoyConfig:
			objMeta = &cecObj.ObjectMeta
			spec = &cecObj.Spec
		case *ciliumv2.CiliumClusterwideEnvoyConfig:
			objMeta = &cecObj.ObjectMeta
			spec = &cecObj.Spec
		}
		resources, err := p.parseResources(
			objMeta.GetNamespace(),
			objMeta.GetName(),
			spec.Resources,
			len(spec.Services) > 0,
			useOriginalSourceAddress(objMeta),
			true,
		)
		if err != nil {
			log.Warn("Skipping CiliumEnvoyConfig due to malformed xDS resources",
				"namespace", objMeta.GetNamespace(),
				"name", objMeta.GetName(),
				logfields.Error, err)
			return nil, false
		}
		return &cec{
			name:      objMeta.GetName(),
			namespace: objMeta.GetNamespace(),
			spec:      spec,
			resources: resources,
		}, true
	}

	// CiliumEnvoyConfig reflection
	err := k8s.RegisterReflector(
		"cec",
		g,
		db,
		tbl,
		k8s.ReflectorConfig[*cec]{
			ListerWatcher: lws.cec,
			Transform:     transform,
			QueryAll: func(txn statedb.ReadTxn, tbl statedb.Table[*cec]) statedb.Iterator[*cec] {
				return statedb.Filter(
					tbl.All(txn),
					func(cec *cec) bool { return cec.namespace != "" },
				)
			},
		},
	)
	if err != nil {
		return err
	}

	// CiliumClusterwideEnvoyConfig reflection
	return k8s.RegisterReflector(
		"ccec",
		g,
		db,
		tbl,
		k8s.ReflectorConfig[*cec]{
			ListerWatcher: lws.ccec,
			Transform:     transform,
			QueryAll: func(txn statedb.ReadTxn, tbl statedb.Table[*cec]) statedb.Iterator[*cec] {
				return statedb.Filter(
					tbl.All(txn),
					func(cec *cec) bool { return cec.namespace == "" },
				)
			},
		},
	)
}

type cecControllerParams struct {
	cell.In

	DB       *statedb.DB
	JobGroup job.Group
	CECs     statedb.Table[*cec]
	Log      *slog.Logger
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

func newCECController(params cecControllerParams) cecControllerOut {
	c := &cecController{params, nil}
	params.JobGroup.Add(job.OneShot("control-loop", c.loop))
	return cecControllerOut{
		C:    c,
		Hook: c.onServiceUpsert,
	}
}

func (c *cecController) setWriter(w *experimental.Writer) {
	c.writer = w
}

func (c *cecController) loop(ctx context.Context, health cell.Health) error {
	var (
		// Services table is used for looking up the service on which we're
		// setting the proxy redirection.
		svcs statedb.Table[*experimental.Service] = c.writer.Services()

		// Iterator for changes (upsert/delete) to the cilium envoy config table.
		// We process each change and look up a referenced service to set/unset the
		// proxy redirection.
		changes statedb.ChangeIterator[*cec]
	)

	{
		wtxn := c.params.DB.WriteTxn(c.params.CECs, c.writer.Services())
		var err error
		changes, err = c.params.CECs.Changes(wtxn)
		wtxn.Commit()
		if err != nil {
			return err
		}
	}

	for {
		txn := c.writer.WriteTxn()
		for change, _, ok := changes.Next(); ok; change, _, ok = changes.Next() {
			cec := change.Object

			for _, svcl := range cec.spec.Services {
				name := loadbalancer.ServiceName{
					Namespace: svcl.Namespace,
					Name:      svcl.Name,
				}
				svc, _, found := svcs.Get(txn, experimental.ServiceByName(name))
				if found {
					svc = svc.Clone()
					if change.Deleted {
						c.params.Log.Debug("Removing proxy redirection",
							"namespace", svcl.Namespace, "name", svcl.Name, "listener", svcl.Listener)
						svc.L7ProxyPort = 0
					} else {
						proxyPort := lookupProxyPort(cec, svcl)
						c.params.Log.Debug("Setting proxy redirection",
							"namespace", svcl.Namespace, "name", svcl.Name, "proxyPort", proxyPort, "listener", svcl.Listener)
						svc.L7ProxyPort = proxyPort
					}
					c.writer.UpsertService(txn, svc)
				}
			}
		}
		txn.Commit()

		select {
		case <-ctx.Done():
			return nil
		case <-changes.Watch(c.params.DB.ReadTxn()):
		}
	}
}

func lookupProxyPort(cec *cec, svcl *ciliumv2.ServiceListener) uint16 {
	if svcl.Listener != "" {
		// Listener names are qualified after parsing, so qualify the listener reference as well for it to match
		svcListener, _ := api.ResourceQualifiedName(
			cec.namespace, cec.name, svcl.Listener, api.ForceNamespace)

		for _, l := range cec.resources.Listeners {
			if l.Name == svcListener {
				if addr := l.GetAddress(); addr != nil {
					if sa := addr.GetSocketAddress(); sa != nil {
						return uint16(sa.GetPortValue())
					}
				}
			}
		}
	}
	return 0
}

// onServiceUpsert is called when the service is upserted, but before it is commited.
// We set the proxy port on the service if there's a matching CEC.
//
// TODO: When loop() sets the ProxyPort we get called back into this. Could consider
// optimizing that out.
func (c *cecController) onServiceUpsert(txn statedb.ReadTxn, svc *experimental.Service) {
	c.params.Log.Info("onServiceUpsert", "name", svc.Name)

	if svc.L7ProxyPort != 0 {
		// Proxy port already set. Changes to proxy port are handled in reconcile()
		return
	}

	// Look up if there is a CiliumEnvoyConfig that references this service.
	cec, _, found := c.params.CECs.Get(txn, cecByServiceName(svc.Name))
	if !found {
		c.params.Log.Info("onServiceUpsert: CEC not found", "name", svc.Name)
		return
	}

	// Find the service listener that referenced this service.
	var svcl *ciliumv2.ServiceListener
	for _, l := range cec.spec.Services {
		if l.Namespace == svc.Name.Namespace && l.Name == svc.Name.Name {
			svcl = l
			break
		}
	}
	if svcl == nil {
		panic("BUG: Table index pointed to a CEC for a service listener, but it was not there.")
	}

	// TODO: We might keep returning proxyPort=0 for this and thus redoing the work here.
	// It might make sense to avoid repeated work by keeping a bit of state, e.g.
	// service name -> CEC revision as this operation doesn't depend on the state of the
	// service itself.

	proxyPort := lookupProxyPort(cec, svcl)
	c.params.Log.Debug("Setting proxy redirection (on service upsert)",
		"namespace", svcl.Namespace,
		"name", svcl.Name,
		"proxyPort", proxyPort,
		"listener", svcl.Listener)
	svc.L7ProxyPort = proxyPort

	return
}
