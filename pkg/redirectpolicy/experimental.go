package redirectpolicy

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/k8s"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/client"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/labels"
	"github.com/cilium/cilium/pkg/k8s/utils"
	k8sUtils "github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/loadbalancer"
	lb "github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/rate"
	"github.com/cilium/cilium/pkg/source"
	"github.com/cilium/cilium/pkg/time"
)

var experimentalCell = cell.Module(
	"local-redirect-policies",
	"Controller for CiliumLocalRedirectPolicy",

	experimentalCells,
	cell.ProvidePrivate(
		newPodListerWatcher,
		newLRPListerWatcher,
	),
)

var experimentalCells = cell.Group(
	cell.ProvidePrivate(
		NewLRPTable,
		statedb.RWTable[*LRPConfig].ToTable,
		NewPodTable, // FIXME this does not belong here
		statedb.RWTable[LocalPod].ToTable,
	),

	cell.Invoke(
		registerLRPReflector,
		registerPodReflector,
		registerLRPController,
	),
)

const (
	LRPTableName = "localredirectpolicies"
)

var (
	lrpUIDIndex = statedb.Index[*LRPConfig, k8s.ServiceID]{
		Name: "id",
		FromObject: func(obj *LRPConfig) index.KeySet {
			// FIXME: Figure out which type to use for k8s object names.
			return index.NewKeySet(index.String(obj.id.String()))
		},
		FromKey: index.Stringer[k8s.ServiceID],
		Unique:  true,
	}

	lrpServiceIndex = statedb.Index[*LRPConfig, k8s.ServiceID]{
		Name: "service",
		FromObject: func(lrp *LRPConfig) index.KeySet {
			if lrp.serviceID == nil {
				return index.KeySet{}
			}
			return index.NewKeySet(index.String(lrp.serviceID.String()))
		},
		FromKey: index.Stringer[k8s.ServiceID],
		Unique:  false,
	}

	lrpAddressIndex = statedb.Index[*LRPConfig, loadbalancer.L3n4Addr]{
		Name: "address",
		FromObject: func(lrp *LRPConfig) index.KeySet {
			if lrp.lrpType != lrpConfigTypeAddr {
				return index.KeySet{}
			}
			keys := make([]index.Key, 0, len(lrp.frontendMappings))
			for _, feM := range lrp.frontendMappings {
				keys = append(keys, feM.feAddr.Bytes())

			}
			return index.NewKeySet(keys...)
		},
		FromKey: func(addr loadbalancer.L3n4Addr) index.Key { return addr.Bytes() },
		Unique:  false,
	}
)

func NewLRPTable(db *statedb.DB) (statedb.RWTable[*LRPConfig], error) {
	tbl, err := statedb.NewTable(
		LRPTableName,
		lrpUIDIndex,
		lrpServiceIndex,
		lrpAddressIndex,
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
	PodUIDIndex = statedb.Index[LocalPod, types.UID]{
		Name: "uid",
		FromObject: func(obj LocalPod) index.KeySet {
			return index.NewKeySet(index.String(string(obj.UID)))
		},
		FromKey: func(uid types.UID) index.Key { return index.String(string(uid)) },
		Unique:  true,
	}
	PodNameIndex = statedb.Index[LocalPod, string]{
		Name: "name",
		FromObject: func(obj LocalPod) index.KeySet {
			return index.NewKeySet(index.String(obj.Namespace + "/" + obj.Name))
		},
		FromKey: index.String,
		// With stateful sets there may be multiple pods with the same name for a short period.
		Unique: false,
	}
)

type podAddr struct {
	lb.L3n4Addr
	portName string
}

type LocalPod struct {
	*slim_corev1.Pod

	L3n4Addrs []podAddr
}

func NewPodTable(db *statedb.DB) (statedb.RWTable[LocalPod], error) {
	tbl, err := statedb.NewTable(
		PodTableName,
		PodUIDIndex,
		PodNameIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

type lrpListerWatcher cache.ListerWatcher

func newLRPListerWatcher(cs client.Clientset) lrpListerWatcher {
	if !cs.IsEnabled() {
		return nil
	}
	return utils.ListerWatcherFromTyped(cs.CiliumV2().CiliumLocalRedirectPolicies("" /* all namespaces */))
}

func registerLRPReflector(db *statedb.DB, jg job.Group, lw lrpListerWatcher, lrps statedb.RWTable[*LRPConfig]) {
	if lw == nil {
		return
	}

	k8s.RegisterReflector("lrps", jg, db, lrps,
		k8s.ReflectorConfig[*LRPConfig]{
			ListerWatcher: lw,
			Transform: func(obj any) (*LRPConfig, bool) {
				clrp, ok := obj.(*ciliumv2.CiliumLocalRedirectPolicy)
				if !ok {
					return nil, false
				}
				rp, err := Parse(clrp, true)
				return rp, err == nil
			},
		})
}

type podListerWatcher cache.ListerWatcher

func newPodListerWatcher(cs client.Clientset) podListerWatcher {
	if !cs.IsEnabled() {
		return nil
	}
	return utils.ListerWatcherWithModifiers(
		utils.ListerWatcherFromTyped(cs.Slim().CoreV1().Pods("")),
		func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.ParseSelectorOrDie("spec.nodeName=" + nodeTypes.GetName()).String()
		})
}

func registerPodReflector(db *statedb.DB, jg job.Group, lw podListerWatcher, pods statedb.RWTable[LocalPod]) {
	if lw == nil {
		return
	}

	k8s.RegisterReflector("pods", jg, db, pods,
		k8s.ReflectorConfig[LocalPod]{
			ListerWatcher: lw,
			Transform: func(obj any) (LocalPod, bool) {
				pod, ok := obj.(*slim_corev1.Pod)
				if !ok {
					return LocalPod{}, false
				}
				return LocalPod{
					Pod:       pod,
					L3n4Addrs: podAddrs(pod),
				}, true
			},
		})
}

type lrpControllerParams struct {
	cell.In

	Log    *slog.Logger
	DB     *statedb.DB
	LRPs   statedb.Table[*LRPConfig]
	Pods   statedb.Table[LocalPod]
	Writer *experimental.Writer
}

type lrpController struct {
	p lrpControllerParams
}

func (h *lrpController) reconciler(ctx context.Context, health cell.Health) error {
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
		// selectedPods maps a pod to the pseudo-service created for the LRP that selected
		// this pod.
		// TODO: part.Map with <pod name>:<lrp name> for a flatter representation?
		selectedPods = map[k8s.ServiceID]sets.Set[loadbalancer.ServiceName]{}

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
		txn = nil

		health.OK("Processing changes")

		// TODO: We're doing fair bit of work with the WriteTxn. This could be split into
		// a change gathering phase and (optimistic) commit phase.
		wtxn := h.p.Writer.WriteTxn()

		// Process the changed local redirect policies. Find frontends that should be redirected
		// and find mathed pods from which to create backends.
		for change, _, ok := lrps.Next(); ok; change, _, ok = lrps.Next() {
			lrp := change.Object
			toName := loadbalancer.ServiceName{Name: lrp.id.Name + "-local-redirect", Namespace: lrp.id.Namespace}

			if change.Deleted {
				err := h.p.Writer.DeleteServiceAndFrontends(wtxn, toName)
				if err != nil {
					h.p.Log.Error("DeleteServiceAndFrontends", "error", err)
				}
			} else {
				// Create a frontendless pseudo-service for the redirect policy. The backends
				// will be associated with this service.
				//
				// TODO: Instead of this pseudo-service we could instead have the backends added
				// to the same service name but marked differently. Not sure what's least confusing.
				h.p.Writer.UpsertService(wtxn,
					&experimental.Service{
						Name:             toName,
						Source:           source.Kubernetes,
						ExtTrafficPolicy: loadbalancer.SVCTrafficPolicyCluster,
						IntTrafficPolicy: loadbalancer.SVCTrafficPolicyCluster,
					},
				)
			}

			switch lrp.lrpType {
			case lrpConfigTypeSvc:
				targetName := loadbalancer.ServiceName{Name: lrp.serviceID.Name, Namespace: lrp.serviceID.Namespace}
				if !change.Deleted {
					h.p.Writer.SetRedirectToByName(wtxn, targetName, &toName)
				} else {
					h.p.Writer.SetRedirectToByName(wtxn, targetName, nil)
				}
			case lrpConfigTypeAddr:
				// TODO: named ports?
				for _, feM := range lrp.frontendMappings {
					if !change.Deleted {
						h.p.Writer.SetRedirectToByAddress(wtxn, *feM.feAddr, &toName)
					} else {
						h.p.Writer.SetRedirectToByAddress(wtxn, *feM.feAddr, nil)
					}
				}
			}

			// Find pods that match with the LRP
			if !change.Deleted {
				iter := h.p.Pods.Prefix(wtxn, PodNameIndex.Query(lrp.id.Namespace))
				for pod, _, ok := iter.Next(); ok; pod, _, ok = iter.Next() {
					if len(pod.Namespace) != len(lrp.id.Namespace) {
						break
					}
					h.processLRPAndPod(wtxn, lrp, pod)
				}
			}
		}

		for change, _, ok := pods.Next(); ok; change, _, ok = pods.Next() {
			// TODO: Avoid repeated work when there are unrelated changes to the pod.
			// e.g. we just care about the L3n4Addrs.

			h.p.Log.Info("Pod change", "change", change)

			pod := change.Object
			id := k8s.ServiceID{Name: pod.Name, Namespace: pod.Namespace}

			if change.Deleted {
				names := selectedPods[id]
				if len(names) == 0 {
					continue
				}
				for _, addr := range pod.L3n4Addrs {
					for name := range names {
						h.p.Writer.ReleaseBackend(wtxn, name, addr.L3n4Addr)
					}
				}
			} else {
				if k8sUtils.GetLatestPodReadiness(pod.Status) != slim_corev1.ConditionTrue {
					continue
				}

				// Find LRPs in the same namespace that select this pod as a backend.
				iter := h.p.LRPs.Prefix(wtxn, lrpUIDIndex.Query(k8s.ServiceID{Namespace: pod.Namespace}))
				for lrp, _, ok := iter.Next(); ok; lrp, _, ok = iter.Next() {
					if len(lrp.id.Namespace) != len(pod.Namespace) {
						// Different (longer) namespace, stop here.
						break
					}
					h.processLRPAndPod(wtxn, lrp, pod)

				}
			}
		}

		for change, _, ok := frontends.Next(); ok; change, _, ok = frontends.Next() {
			fe := change.Object
			if change.Deleted {
				continue
			}

			serviceID := k8s.ServiceID{
				Namespace: fe.ServiceName.Namespace,
				Name:      fe.ServiceName.Name,
			}

			lrp, _, found := h.p.LRPs.Get(wtxn, lrpServiceIndex.Query(serviceID))
			if !found {
				lrp, _, found = h.p.LRPs.Get(wtxn, lrpAddressIndex.Query(fe.Address))
			}
			if !found {
				continue
			}

			// FIXME port name matching againts frontendMappings.

			// A local redirect policy matches this frontend, set the redirect.
			toName := loadbalancer.ServiceName{Name: lrp.id.Name + "-local-redirect", Namespace: lrp.id.Namespace}
			h.p.Writer.SetRedirectToByAddress(wtxn, fe.Address, &toName)
		}

		wtxn.Commit()
	}
}

func podAddrs(pod *slim_corev1.Pod) (addrs []podAddr) {
	podIPs := k8sUtils.ValidIPs(pod.Status)
	if len(podIPs) == 0 {
		// IPs not available yet.
		return nil
	}
	for _, podIP := range podIPs {
		addrCluster, err := cmtypes.ParseAddrCluster(podIP)
		if err != nil {
			continue
		}
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == "" {
					continue
				}
				l4addr := lb.NewL4Addr(lb.L4Type(port.Protocol), uint16(port.ContainerPort))
				addr := podAddr{
					L3n4Addr: lb.L3n4Addr{
						AddrCluster: addrCluster,
						L4Addr:      *l4addr,
						Scope:       0,
					},
					portName: port.Name,
				}
				addrs = append(addrs, addr)
			}
		}
	}
	return
}

func (h *lrpController) processLRPAndPod(wtxn experimental.WriteTxn, lrp *LRPConfig, pod LocalPod) {
	h.p.Log.Info("processLRPAndPod",
		"lrp", lrp.id,
		"pod", pod.Name)

	labelSet := labels.Set(pod.GetLabels())
	if !lrp.backendSelector.Matches(labelSet) {
		h.p.Log.Info("Mismatching selector", "backendSelector", lrp.backendSelector, "labelSet", labelSet)
		return
	}

	portNameMatches := func(portName string) bool {
		for bePortName := range lrp.backendPortsByPortName {
			if bePortName == portName {
				return true
			}
		}
		return false
	}

	// Port name checks can be skipped in certain cases.
	switch {
	case lrp.frontendType == svcFrontendAll:
		fallthrough
	case lrp.frontendType == svcFrontendSinglePort:
		fallthrough
	case lrp.frontendType == addrFrontendSinglePort:
		portNameMatches = nil

	}

	// TODO: Store the generated name in LRPConfig?
	toName := loadbalancer.ServiceName{Name: lrp.id.Name + "-local-redirect", Namespace: lrp.id.Namespace}

	for _, addr := range pod.L3n4Addrs {
		if portNameMatches != nil && !portNameMatches(addr.portName) {
			h.p.Log.Info("Mismatching port name", "portName", addr.portName, "frontendType", lrp.frontendType)
			continue
		}
		be := experimental.BackendParams{
			L3n4Addr: addr.L3n4Addr,
			State:    lb.BackendStateActive,
			PortName: addr.portName,
		}
		h.p.Log.Info("Adding backend instance", "name", toName)
		h.p.Writer.UpsertBackends(
			wtxn,
			toName,
			source.Kubernetes,
			be,
		)
	}
}

func registerLRPController(g job.Group, p lrpControllerParams) {
	h := &lrpController{p}
	g.Add(job.OneShot("reconciler", h.reconciler))
}

func (lrp *LRPConfig) TableHeader() []string {
	return []string{
		"ID",
		"Type",
		"FrontendType",
		"Mappings",
	}
}
func (lrp *LRPConfig) TableRow() []string {
	m := lrp.GetModel()
	mappings := make([]string, 0, len(m.FrontendMappings))
	for _, feM := range m.FrontendMappings {
		addr := feM.FrontendAddress
		mappings = append(mappings,
			fmt.Sprintf("%s:%d %s", addr.IP, addr.Port, addr.Protocol))
	}
	return []string{
		m.Namespace + "/" + m.Name,
		m.LrpType,
		m.FrontendType,
		strings.Join(mappings, ", "),
	}

}
