package experimental

import (
	"context"
	"log/slog"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"

	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/k8s"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/client"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/loadbalancer"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/rate"
	"github.com/cilium/cilium/pkg/source"
	"github.com/cilium/cilium/pkg/time"
)

var LRPHandlerCell = cell.Module(
	"lrp-handler",
	"LocalRedirectPolicy handler",

	cell.ProvidePrivate(
		NewLRPTable,
		NewPodTable, // FIXME this does not belong here
	),
	cell.Provide(
		// Provide Table[*CiliumLocalRedirectPolicy] for read-only access from other
		// modules.
		statedb.RWTable[*ciliumv2.CiliumLocalRedirectPolicy].ToTable,
	),

	cell.Invoke(
		registerLRPReflector,
		registerPodReflector,
		registerLRPHandler,
	),
)

const (
	LRPTableName = "localredirectpolicies"
)

var (
	LRPNameIndex = statedb.Index[*ciliumv2.CiliumLocalRedirectPolicy, string]{
		Name: "name",
		FromObject: func(obj *ciliumv2.CiliumLocalRedirectPolicy) index.KeySet {
			// FIXME: Figure out which type to use for k8s object names.
			return index.NewKeySet(index.String(obj.Namespace + "/" + obj.Name))
		},
		FromKey: index.String,
		Unique:  true,
	}

	LRPServiceNameIndex = statedb.Index[*ciliumv2.CiliumLocalRedirectPolicy, loadbalancer.ServiceName]{
		Name: "service-name",
		FromObject: func(lrp *ciliumv2.CiliumLocalRedirectPolicy) index.KeySet {
			if lrp.Spec.RedirectFrontend.ServiceMatcher != nil {
				return index.NewKeySet(index.String(
					lrp.Spec.RedirectFrontend.ServiceMatcher.Namespace + "/" +
						lrp.Spec.RedirectFrontend.ServiceMatcher.Name))
			}
			return index.KeySet{}
		},
		FromKey: index.Stringer[loadbalancer.ServiceName],
		Unique:  false,
	}

	LRPAddressIndex = statedb.Index[*ciliumv2.CiliumLocalRedirectPolicy, loadbalancer.L3n4Addr]{
		Name: "service-name",
		FromObject: func(lrp *ciliumv2.CiliumLocalRedirectPolicy) index.KeySet {
			addrMatcher := lrp.Spec.RedirectFrontend.AddressMatcher
			if addrMatcher == nil {
				return index.KeySet{}
			}
			addrCluster, err := cmtypes.ParseAddrCluster(addrMatcher.IP)
			if err != nil {
				return index.KeySet{}
			}
			checkNamedPort := false
			if len(addrMatcher.ToPorts) > 1 {
				// If there are multiple ports, then the ports must be named.
				checkNamedPort = true
			}

			keys := []index.Key{}
			for _, portInfo := range addrMatcher.ToPorts {
				p, _, proto, err := portInfo.SanitizePortInfo(checkNamedPort)
				if err != nil {
					return index.KeySet{}
				}
				// Set the scope to ScopeExternal as the externalTrafficPolicy is set to Cluster.
				fe := loadbalancer.NewL3n4Addr(proto, addrCluster, p, loadbalancer.ScopeExternal)
				keys = append(keys, fe.Bytes())
			}
			return index.NewKeySet(keys...)
		},
		FromKey: func(addr loadbalancer.L3n4Addr) index.Key { return addr.Bytes() },
		Unique:  false,
	}
)

func NewLRPTable(db *statedb.DB) (statedb.RWTable[*ciliumv2.CiliumLocalRedirectPolicy], error) {
	tbl, err := statedb.NewTable(
		LRPTableName,
		LRPNameIndex,
		LRPServiceNameIndex,
		LRPAddressIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

const (
	PodTableName = "k8s-pods"
)

var (
	PodNameIndex = statedb.Index[*slim_corev1.Pod, string]{
		Name: "name",
		FromObject: func(obj *slim_corev1.Pod) index.KeySet {
			// FIXME: Figure out which type to use for k8s object names.
			return index.NewKeySet(index.String(obj.Namespace + "/" + obj.Name))
		},
		FromKey: index.String,
		Unique:  true,
	}
)

func NewPodTable(db *statedb.DB) (statedb.RWTable[*slim_corev1.Pod], error) {
	tbl, err := statedb.NewTable(
		PodTableName,
		PodNameIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

func registerLRPReflector(db *statedb.DB, jg job.Group, client client.Clientset, lrps statedb.RWTable[*ciliumv2.CiliumLocalRedirectPolicy]) {
	if !client.IsEnabled() {
		return
	}
	k8s.RegisterReflector(jg, db,
		k8s.ReflectorConfig[*ciliumv2.CiliumLocalRedirectPolicy]{
			ListerWatcher: utils.ListerWatcherFromTyped(client.CiliumV2().CiliumLocalRedirectPolicies("" /* all namespaces */)),
			Table:         lrps,
		})
}

func registerPodReflector(db *statedb.DB, jg job.Group, client client.Clientset, pods statedb.RWTable[*slim_corev1.Pod]) {
	if !client.IsEnabled() {
		return
	}
	podLW := utils.ListerWatcherWithModifiers(
		utils.ListerWatcherFromTyped(client.Slim().CoreV1().Pods("")),
		func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.ParseSelectorOrDie("spec.nodeName=" + nodeTypes.GetName()).String()
		})
	k8s.RegisterReflector(jg, db,
		k8s.ReflectorConfig[*slim_corev1.Pod]{
			ListerWatcher: podLW,
			Table:         pods,
		})
}

type lrpHandlerParams struct {
	cell.In

	Log    *slog.Logger
	DB     *statedb.DB
	LRPs   statedb.Table[*ciliumv2.CiliumLocalRedirectPolicy]
	Pods   statedb.Table[*slim_corev1.Pod]
	Writer *Writer
}

type lrpHandler struct {
	p lrpHandlerParams
}

func (h *lrpHandler) reconciler(ctx context.Context, health cell.Health) error {
	// Start tracking upserts and deletions of LRPs and Pods.
	wtxn := h.p.DB.WriteTxn(h.p.LRPs, h.p.Pods, h.p.Writer.Frontends())
	defer wtxn.Abort()
	lrps, err := h.p.LRPs.Changes(wtxn)
	if err != nil {
		return err
	}
	defer lrps.Close()
	pods, err := h.p.Pods.Changes(wtxn)
	if err != nil {
		return err
	}
	defer pods.Close()

	frontends, err := h.p.Writer.Frontends().Changes(wtxn)
	if err != nil {
		return err
	}
	defer frontends.Close()

	wtxn.Commit()

	var (
		// selectedPods maps from pod name to the local redirect policy serice name.
		selectedPods = map[k8s.ServiceID]loadbalancer.ServiceName{}

		limiter = rate.NewLimiter(100*time.Millisecond, 1)
	)

	for {
		health.OK("Waiting for changes")

		// Rate limit the processing of the changes to increase efficiency.
		if err := limiter.Wait(ctx); err != nil {
			// Context cancelled, shutting down.
			return nil
		}

		// Refresh and wait for more changes.
		txn := h.p.DB.ReadTxn()
		select {
		case <-lrps.Watch(txn):
		case <-pods.Watch(txn):
		case <-frontends.Watch(txn):
		case <-ctx.Done():
			return nil
		}

		health.OK("Processing changes")

		// TODO: We're doing fair bit of work with the WriteTxn. This could be split into
		// a change gathering phase and commit phase.
		wtxn := h.p.Writer.WriteTxn()

		// Process the changed local redirect policies. Find frontends that should be redirected
		// and find mathed pods from which to create backends.
		for change, _, ok := lrps.Next(); ok; change, _, ok = lrps.Next() {
			lrp := change.Object
			toName := loadbalancer.ServiceName{Name: lrp.Name + "-local-redirect", Namespace: lrp.Namespace}

			if change.Deleted {
				h.p.Writer.DeleteServiceAndFrontends(wtxn, toName)
			} else {
				// Create a frontendless pseudo-service for the redirect policy. The backends
				// will be associated with this service.
				//
				// TODO: Instead of this pseudo-service we could instead have the backends added
				// to the same service name but marked differently. Not sure what's least confusing.
				h.p.Writer.UpsertService(wtxn,
					&Service{
						Name:             toName,
						Source:           source.Kubernetes,
						ExtTrafficPolicy: loadbalancer.SVCTrafficPolicyCluster,
						IntTrafficPolicy: loadbalancer.SVCTrafficPolicyCluster,
					},
				)
			}

			if svcMatcher := lrp.Spec.RedirectFrontend.ServiceMatcher; svcMatcher != nil {
				name := loadbalancer.ServiceName{Name: svcMatcher.Name, Namespace: svcMatcher.Namespace}
				if change.Deleted {
					h.p.Writer.SetRedirectToByName(wtxn, name, nil)
					h.p.Writer.ReleaseBackendsForService(wtxn, toName)
				} else {
					h.p.Writer.SetRedirectToByName(wtxn, name, &toName)
				}
			}

			if addrMatcher := lrp.Spec.RedirectFrontend.AddressMatcher; addrMatcher != nil {
				// FIXME: Parse the CiliumLocalRedirectPolicy into a sanitized internal model
				// at the ingestion phase.
				addrCluster, err := cmtypes.ParseAddrCluster(addrMatcher.IP)
				if err == nil {
					for _, portInfo := range addrMatcher.ToPorts {
						// FIXME named ports
						p, _, proto, err := portInfo.SanitizePortInfo(false)
						if err == nil {
							fe := loadbalancer.NewL3n4Addr(proto, addrCluster, p, loadbalancer.ScopeExternal)
							h.p.Writer.SetRedirectToByAddress(wtxn, *fe, &toName)
						}
					}
				}
			}
		}

		for change, _, ok := pods.Next(); ok; change, _, ok = pods.Next() {
			// TODO: Find LRPs selecting these pods and create backends for them.
			pod := change.Object
			id := k8s.ServiceID{Name: pod.Name, Namespace: pod.Namespace}
			svcName, selected := selectedPods[id]

			switch {
			case change.Deleted && selected:
				h.p.Writer.ReleaseBackendsForService(wtxn, svcName)

			case !change.Deleted:
				// TODO: Find if any LRP matches with this pod.
				//selector := api.NewESFromK8sLabelSelector("", &redirectTo.LocalEndpointSelector)
			}
		}

		for change, _, ok := frontends.Next(); ok; change, _, ok = frontends.Next() {
			fe := change.Object
			if change.Deleted {
				continue
			}

			lrp, _, found := h.p.LRPs.Get(wtxn, LRPServiceNameIndex.Query(fe.ServiceName))
			if !found {
				lrp, _, found = h.p.LRPs.Get(wtxn, LRPAddressIndex.Query(fe.Address))
			}
			if !found {
				continue
			}

			// A local redirect policy matches this frontend, set the redirect.
			toName := loadbalancer.ServiceName{Name: lrp.Name + "-local-redirect", Namespace: lrp.Namespace}
			h.p.Writer.SetRedirectToByAddress(wtxn, fe.Address, &toName)
		}

		wtxn.Commit()
	}
}

func registerLRPHandler(g job.Group, p lrpHandlerParams) {
	h := &lrpHandler{p}
	g.Add(job.OneShot("lrp-reconciler", h.reconciler))
}
