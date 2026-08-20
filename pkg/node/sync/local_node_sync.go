// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package sync

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/netip"

	"github.com/cilium/hive/cell"
	k8stypes "k8s.io/apimachinery/pkg/types"

	agentK8s "github.com/cilium/cilium/daemon/k8s"
	"github.com/cilium/cilium/pkg/annotation"
	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	ipsec "github.com/cilium/cilium/pkg/datapath/linux/ipsec/types"
	"github.com/cilium/cilium/pkg/datapath/tunnel"
	v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/labelsfilter"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/node/addressing"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/source"
)

var LocalNodeSyncCell = cell.Module(
	"local-node-sync",
	"Provides LocalNodeSynchronizer that syncs the LocalNodeStore with the K8s Node",

	// Provides a newLocalNodeSynchronizer that is invoked when LocalNodeStore is started.
	// This fills in the initial state before it is accessed by other sub-systems.
	// Then, it takes care of keeping selected fields (e.g., labels, annotations)
	// synchronized with the corresponding kubernetes object.
	cell.Provide(newLocalNodeSynchronizer),
)

// InitFunc is called during startup to fill in the local node before other
// sub-systems can access it. This is called after the node is filled in
// from configuration and k8s node.
type InitFunc func(context.Context, *node.LocalNodeMutator) error

type parsedNode struct {
	name                string
	addresses           []node.Address
	ipv4AllocCIDR       netip.Prefix
	ipv6AllocCIDR       netip.Prefix
	labels, annotations map[string]string
	uid                 k8stypes.UID
	providerID          string
}

type localNodeSynchronizerParams struct {
	cell.In

	Logger             *slog.Logger
	Config             *option.DaemonConfig
	ClusterInfo        cmtypes.ClusterInfo
	TunnelConfig       tunnel.Config
	K8sLocalNode       agentK8s.LocalNodeResource
	K8sCiliumLocalNode agentK8s.LocalCiliumNodeResource
	IPsecConfig        ipsec.Config
	ExtraInitFuncs     []InitFunc `group:"init-funcs"`
}

// localNodeSynchronizer performs the bootstrapping of the LocalNodeStore,
// which contains information about the local Cilium node populated from
// configuration and Kubernetes. Additionally, it also takes care of keeping
// the selected fields of the LocalNodeStore synchronized with Kubernetes.
type localNodeSynchronizer struct {
	localNodeSynchronizerParams
	old parsedNode
}

func (ini *localNodeSynchronizer) InitLocalNode(ctx context.Context, n *node.LocalNodeMutator) error {
	n.SetIdentity(nodeTypes.GetName(), ini.ClusterInfo.Name, ini.ClusterInfo.ID, source.Local)
	if err := ini.initFromConfig(n); err != nil {
		return err
	}

	n.UpdateLocalInfo(func(info *node.LocalNodeInfo) {
		info.UnderlayProtocol = ini.TunnelConfig.UnderlayProtocol()
	})

	if err := ini.initFromK8s(ctx, n); err != nil {
		return err
	}

	bootID := node.GetBootID(ini.Logger)
	if ini.IPsecConfig.Enabled() && bootID == "" {
		return fmt.Errorf("IPSec requires a valid BootID")
	}
	n.SetBootID(bootID)

	for _, fn := range ini.ExtraInitFuncs {
		if err := fn(ctx, n); err != nil {
			return err
		}
	}

	return nil
}

func (ini *localNodeSynchronizer) SyncLocalNode(ctx context.Context, store *node.LocalNodeStore) {
	if ini.K8sLocalNode == nil {
		return
	}

	for ev := range ini.K8sLocalNode.Events(ctx) {
		if ev.Kind == resource.Upsert {
			ini.Logger.Debug("Received Local Node upsert event", logfields.Node, ev.Object)
			isBeingDeleted := ev.Object.DeletionTimestamp != nil
			if isBeingDeleted {
				// Update LocalNode to mark it as being deleted
				store.UpdateLocalInfo(func(info *node.LocalNodeInfo) {
					info.IsBeingDeleted = true
				})
			}
			new := parseNode(ini.Logger, ev.Object)
			if !ini.mutableFieldsEqual(new) {
				store.Update(func(n *node.LocalNodeMutator) {
					ini.syncFromK8s(n, new)
				})
			}
		} else if ev.Kind == resource.Delete {
			ini.Logger.Info("Received Local node Delete event", logfields.Node, ev.Object)
			// Mark as being deleted on explicit delete events too
			store.UpdateLocalInfo(func(info *node.LocalNodeInfo) {
				info.IsBeingDeleted = true
			})
		}

		ev.Done(nil)
	}
}

func newLocalNodeSynchronizer(p localNodeSynchronizerParams) node.LocalNodeSynchronizer {
	return &localNodeSynchronizer{
		localNodeSynchronizerParams: p,
	}
}

func (ini *localNodeSynchronizer) initFromConfig(n *node.LocalNodeMutator) error {
	n.UpdateLocalInfo(func(info *node.LocalNodeInfo) {
		info.IPv4NativeRoutingCIDR = ini.Config.IPv4NativeRoutingCIDR
		info.IPv6NativeRoutingCIDR = ini.Config.IPv6NativeRoutingCIDR
	})

	// Initialize node IP addresses from configuration.
	if ini.Config.IPv6NodeAddr != "auto" {
		if ip := net.ParseIP(ini.Config.IPv6NodeAddr); ip == nil {
			return fmt.Errorf("invalid IPv6 node address: %q", ini.Config.IPv6NodeAddr)
		} else {
			if !ip.IsGlobalUnicast() {
				return fmt.Errorf("Invalid IPv6 node address: %q not a global unicast address", ip)
			}
			addr, _ := netip.AddrFromSlice(ip)
			n.SetAddress(node.AddressKindNode, addressing.NodeInternalIP, true, addr)
		}
	}
	if ini.Config.IPv4NodeAddr != "auto" {
		if ip := net.ParseIP(ini.Config.IPv4NodeAddr); ip == nil {
			return fmt.Errorf("Invalid IPv4 node address: %q", ini.Config.IPv4NodeAddr)
		} else {
			addr, _ := netip.AddrFromSlice(ip)
			n.SetAddress(node.AddressKindNode, addressing.NodeInternalIP, false, addr)
		}
	}
	return nil
}

func (ini *localNodeSynchronizer) getK8sLocalNode(ctx context.Context) (*slim_corev1.Node, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for ev := range ini.K8sLocalNode.Events(ctx) {
		ev.Done(nil)
		if ev.Kind == resource.Upsert {
			return ev.Object, nil
		}
	}
	return nil, ctx.Err()
}

// getK8sLocalCiliumNode returns the CiliumNode object for the local node if it exists at the type
// of the call.
// In the case that the resource event is synced without a ciliumnode upsert event, we return nil.
func (ini *localNodeSynchronizer) getK8sLocalCiliumNode(ctx context.Context) *v2.CiliumNode {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		return nil
	case ev := <-ini.K8sCiliumLocalNode.Events(ctx):
		ev.Done(nil)
		switch ev.Kind {
		case resource.Upsert:
			return ev.Object
		case resource.Sync:
			ini.Logger.Debug("sync event received before local ciliumnode upsert, skipping ciliumnode sync")
			return nil
		}
	}
	return nil
}

func (ini *localNodeSynchronizer) initFromK8s(ctx context.Context, n *node.LocalNodeMutator) error {
	if ini.K8sLocalNode == nil {
		return nil
	}

	k8sNode, err := ini.getK8sLocalNode(ctx)
	if err != nil {
		return err
	}
	parsedNode := parseNode(ini.Logger, k8sNode)

	// Initialize the fields in local node where the source of truth is in Kubernetes.
	// Later stages will deal with updating rest of the fields depending on configuration.
	//
	// The fields left uninitialized/unrestored here:
	//   - Cilium internal IPs (restored from cilium_host or allocated by IPAM)
	//   - Health IPs (allocated by IPAM)
	//   - Ingress IPs (restored from ipcachemap or allocated)
	//   - WireGuard key (set by WireGuard agent)
	//   - IPsec key (set by IPsec)
	//   - alloc CIDRs (depends on IPAM mode; restored from Node or CiliumNode)
	n.SetIdentity(parsedNode.name, n.Cluster(), n.ClusterID(), n.Source())
	for _, address := range parsedNode.addresses {
		n.SetAddress(node.AddressKindNode, address.NodeAddressType,
			address.Addr().Is6(), address.Addr())
	}
	ini.syncFromK8s(n, parsedNode)

	// In cases where no local CiliumNode exists (such as on a fresh node) we skip restoring
	// the CiliumNode information from k8s.
	k8sCiliumNode := ini.getK8sLocalCiliumNode(ctx)
	if k8sCiliumNode != nil {
		for _, address := range k8sCiliumNode.Spec.Addresses {
			if address.Type == addressing.NodeCiliumInternalIP {
				if addr, err := netip.ParseAddr(address.IP); err == nil {
					n.SetAddress(node.AddressKindNode,
						addressing.NodeCiliumInternalIP, addr.Is6(), addr)
				}
			}
		}

		if ini.Config.EnableHealthChecking && ini.Config.EnableEndpointHealthChecking {
			if ini.Config.EnableIPv4 {
				addr, _ := netip.ParseAddr(k8sCiliumNode.Spec.HealthAddressing.IPv4)
				n.SetAddress(node.AddressKindHealth, "", false, addr)
			}

			if ini.Config.EnableIPv6 {
				addr, _ := netip.ParseAddr(k8sCiliumNode.Spec.HealthAddressing.IPv6)
				n.SetAddress(node.AddressKindHealth, "", true, addr)
			}
		}
	} else {
		ini.Logger.Info("no local ciliumnode found, will not restore cilium internal and health ips from k8s")
	}

	return nil
}

func (ini *localNodeSynchronizer) mutableFieldsEqual(new parsedNode) bool {
	return maps.Equal(ini.old.labels, new.labels) &&
		maps.Equal(ini.old.annotations, new.annotations) &&
		ini.old.uid == new.uid && ini.old.providerID == new.providerID
}

// syncFromK8s synchronizes the fields that can be mutated at runtime
func (ini *localNodeSynchronizer) syncFromK8s(n *node.LocalNodeMutator, new parsedNode) {
	filter := func(old, new map[string]string, key string) bool {
		_, oldExists := old[key]
		_, newExists := new[key]
		return oldExists && !newExists
	}

	labels := maps.Collect(n.Labels())
	ini.Logger.Debug(
		"Syncing local node with new labels",
		logfields.NodeLabels, labels,
		logfields.OldLabels, ini.old.labels,
		logfields.NewLabels, new.labels,
	)

	maps.DeleteFunc(labels, func(key, _ string) bool { return filter(ini.old.labels, new.labels, key) })
	maps.Copy(labels, new.labels)
	n.SetLabels(labels)

	annotations := maps.Collect(n.Annotations())
	ini.Logger.Debug(
		"Syncing local node with new annotations",
		logfields.Annotations, annotations,
		logfields.OldAnnotations, ini.old.annotations,
		logfields.NewAnnotations, new.annotations,
	)

	maps.DeleteFunc(annotations, func(key, _ string) bool { return filter(ini.old.annotations, new.annotations, key) })
	maps.Copy(annotations, new.annotations)
	n.SetAnnotations(annotations)
	n.UpdateLocalInfo(func(info *node.LocalNodeInfo) {
		info.UID = new.uid
		info.ProviderID = new.providerID
	})
	ini.old = new

	ini.Logger.Debug(
		"Local node UID and ProviderID updated",
		logfields.UID, new.uid,
		logfields.ProviderID, new.providerID,
	)
}

func parseNode(logger *slog.Logger, k8sNode *slim_corev1.Node) parsedNode {
	parsed := parsedNode{
		name: k8sNode.Name,
		labels: labelsfilter.FilterLabelsByRegex(
			option.Config.ExcludeNodeLabelPatterns, k8sNode.GetLabels()),
		annotations: map[string]string{},
		uid:         k8sNode.GetUID(), providerID: k8sNode.Spec.ProviderID,
	}
	for key, value := range k8sNode.GetAnnotations() {
		if annotation.CiliumPrefixRegex.MatchString(key) {
			parsed.annotations[key] = value
		}
	}

	type addressSlot struct {
		typ  addressing.AddressType
		ipv6 bool
	}
	seen := map[addressSlot]struct{}{}
	for _, address := range k8sNode.Status.Addresses {
		var typ addressing.AddressType
		switch address.Type {
		case slim_corev1.NodeInternalIP:
			typ = addressing.NodeInternalIP
		case slim_corev1.NodeExternalIP:
			typ = addressing.NodeExternalIP
		default:
			continue
		}
		addr, err := netip.ParseAddr(address.Address)
		if err != nil {
			logger.Warn("Ignoring invalid node IP", logfields.IPAddr, address.Address,
				logfields.Type, address.Type)
			continue
		}
		addr = addr.Unmap()
		slot := addressSlot{typ, addr.Is6()}
		if _, found := seen[slot]; found {
			logger.Warn("Detected multiple IPs of the same address type and family, Cilium will only consider the first IP in the Node resource",
				logfields.Type, address.Type)
			continue
		}
		seen[slot] = struct{}{}
		parsed.addresses = append(parsed.addresses, node.Address{
			Kind: node.AddressKindNode, NodeAddressType: typ,
			Prefix: netip.PrefixFrom(addr, addr.BitLen()),
		})
	}
	podCIDRs := k8sNode.Spec.PodCIDRs
	if len(podCIDRs) == 0 && k8sNode.Spec.PodCIDR != "" {
		podCIDRs = []string{k8sNode.Spec.PodCIDR}
	}
	for _, raw := range podCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			logger.Warn("Invalid PodCIDR value for node", logfields.Error, err,
				logfields.PodCIDRs, podCIDRs)
			continue
		}
		if prefix.Addr().Is4() {
			parsed.ipv4AllocCIDR = prefix
		} else {
			parsed.ipv6AllocCIDR = prefix
		}
	}
	if option.Config.AnnotateK8sNode {
		if !parsed.ipv4AllocCIDR.IsValid() {
			if raw, ok := annotation.Get(k8sNode, annotation.V4CIDRName, annotation.V4CIDRNameAlias); ok {
				parsed.ipv4AllocCIDR, _ = netip.ParsePrefix(raw)
			}
		}
		if !parsed.ipv6AllocCIDR.IsValid() {
			if raw, ok := annotation.Get(k8sNode, annotation.V6CIDRName, annotation.V6CIDRNameAlias); ok {
				parsed.ipv6AllocCIDR, _ = netip.ParsePrefix(raw)
			}
		}
	}
	return parsed
}
