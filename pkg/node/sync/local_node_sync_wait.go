// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package sync

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"

	ipamOption "github.com/cilium/cilium/pkg/ipam/option"
	k8sConst "github.com/cilium/cilium/pkg/k8s/constants"
	"github.com/cilium/cilium/pkg/k8s/resource"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/node/addressing"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/time"
)

func (ini *localNodeSynchronizer) retrieveNodeInformation(ctx context.Context) *parsedNode {
	var n *parsedNode
	waitForCIDR := func() error {
		if option.Config.K8sRequireIPv4PodCIDR && !n.ipv4AllocCIDR.IsValid() {
			return fmt.Errorf("required IPv4 PodCIDR not available")
		}
		if option.Config.K8sRequireIPv6PodCIDR && !n.ipv6AllocCIDR.IsValid() {
			return fmt.Errorf("required IPv6 PodCIDR not available")
		}
		return nil
	}

	if option.Config.IPAM == ipamOption.IPAMClusterPool ||
		option.Config.IPAM == ipamOption.IPAMMultiPool {
		for event := range ini.K8sCiliumLocalNode.Events(ctx) {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				ini.Logger.Error("Timeout while waiting for CiliumNode resource: API server connection issue", logfields.NodeName, nodeTypes.GetName())
				break
			}
			if event.Kind == resource.Upsert {
				no := parsedNodeFromData(event.Object.NodeData(ini.ClusterInfo))
				n = &no
				ini.Logger.Info("Retrieved node information from cilium node", logfields.NodeName, n.name)
				if err := waitForCIDR(); err != nil {
					ini.Logger.Warn("Waiting for k8s node information", logfields.Error, err)
				} else {
					event.Done(nil)
					break
				}
			}
			event.Done(nil)
		}
	} else {
		for event := range ini.K8sLocalNode.Events(ctx) {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				ini.Logger.Error("Timeout while waiting for Node resource: API server connection issue", logfields.NodeName, nodeTypes.GetName())
				break
			}
			if event.Kind == resource.Upsert {
				no := parseNode(ini.Logger, event.Object)
				n = &no
				ini.Logger.Info("Retrieved node information from kubernetes node", logfields.NodeName, n.name)
				if err := waitForCIDR(); err != nil {
					ini.Logger.Warn("Waiting for k8s node information", logfields.Error, err)
				} else {
					event.Done(nil)
					break
				}
			}
			event.Done(nil)
		}
	}

	return n
}

// WaitForNodeInformation retrieves the node information via the CiliumNode or
// Kubernetes Node resource. This function will block until the information is
// received.
func (ini *localNodeSynchronizer) WaitForNodeInformation(ctx context.Context, store *node.LocalNodeStore) error {
	// Use of the environment variable overwrites the node-name
	// automatically derived
	nodeName := nodeTypes.GetName()
	if nodeName == "" {
		if option.Config.K8sRequireIPv4PodCIDR || option.Config.K8sRequireIPv6PodCIDR {
			return fmt.Errorf("node name must be specified via environment variable '%s' to retrieve Kubernetes PodCIDR range", k8sConst.EnvNodeNameSpec)
		}
		ini.Logger.Info("K8s node name is empty. BPF NodePort might not be able to auto detect all devices")
		return nil
	}

	requireIPv4CIDR := option.Config.K8sRequireIPv4PodCIDR
	requireIPv6CIDR := option.Config.K8sRequireIPv6PodCIDR
	// If no CIDR is required, retrieving the node information is
	// optional
	// At this point it's not clear whether the device auto-detection will
	// happen, as initKubeProxyReplacementOptions() might disable BPF NodePort.
	// Anyway, to be on the safe side, don't give up waiting for a (Cilium)Node
	// self object.
	isNodeInformationOptional := (!requireIPv4CIDR && !requireIPv6CIDR)
	// If node information is optional, let's wait 10 seconds only.
	// It node information is required, wait indefinitely.
	if isNodeInformationOptional {
		newCtx, cancel := context.WithTimeout(ctx, time.Second*10)
		ctx = newCtx
		defer cancel()
	}

	if n := ini.retrieveNodeInformation(ctx); n != nil {
		nodeIP4 := n.nodeIP(false)
		nodeIP6 := n.nodeIP(true)
		k8sNodeIP := n.k8sNodeIP()

		ini.Logger.Info(
			"Received own node information from API server",
			logfields.NodeName, n.name,
			logfields.Labels, n.labels,
			logfields.IPv4, nodeIP4,
			logfields.IPv6, nodeIP6,
			logfields.V4Prefix, n.ipv4AllocCIDR,
			logfields.V6Prefix, n.ipv6AllocCIDR,
			logfields.K8sNodeIP, k8sNodeIP,
		)

		if option.Config.EnableIPv6 && nodeIP6 == nil {
			ini.Logger.Warn("IPv6 is enabled, but Cilium cannot find the IPv6 address for this node. " +
				"This may cause connectivity disruption for Endpoints that attempt to communicate using IPv6")
		}

		// Set allocation CIDRs
		if n.ipv4AllocCIDR.IsValid() && option.Config.EnableIPv4 {
			store.Update(func(local *node.LocalNodeMutator) {
				local.SetAllocationCIDRs(false, n.ipv4AllocCIDR)
			})
		}
		if n.ipv6AllocCIDR.IsValid() && option.Config.EnableIPv6 {
			store.Update(func(local *node.LocalNodeMutator) {
				local.SetAllocationCIDRs(true, n.ipv6AllocCIDR)
			})
		}
	} else {
		// if node resource could not be received, fail if
		// PodCIDR requirement has been requested
		if requireIPv4CIDR || requireIPv6CIDR {
			return fmt.Errorf("unable to derive PodCIDR via Node or CiliumNode resource")
		}
	}

	// Annotate addresses will occur later since the user might
	// want to specify them manually
	return nil
}

func parsedNodeFromData(data node.Data) parsedNode {
	n := parsedNode{
		name: data.Name(), labels: maps.Collect(data.Labels()),
		annotations: maps.Collect(data.Annotations()),
	}
	for address := range data.Addresses() {
		switch address.Kind {
		case node.AddressKindNode:
			n.addresses = append(n.addresses, address)
		case node.AddressKindAllocation:
			if address.Addr().Is4() && (!n.ipv4AllocCIDR.IsValid() || address.Primary) {
				n.ipv4AllocCIDR = address.Prefix
			} else if address.Addr().Is6() && (!n.ipv6AllocCIDR.IsValid() || address.Primary) {
				n.ipv6AllocCIDR = address.Prefix
			}
		}
	}
	return n
}

func (n parsedNode) nodeIP(ipv6 bool) net.IP {
	var fallback netip.Addr
	for _, address := range n.addresses {
		if address.Addr().Is6() != ipv6 {
			continue
		}
		if address.NodeAddressType == addressing.NodeInternalIP {
			return address.Addr().AsSlice()
		}
		if !fallback.IsValid() {
			fallback = address.Addr()
		}
	}
	if fallback.IsValid() {
		return fallback.AsSlice()
	}
	return nil
}

func (n parsedNode) k8sNodeIP() net.IP {
	var external netip.Addr
	for _, address := range n.addresses {
		switch address.NodeAddressType {
		case addressing.NodeInternalIP:
			return address.Addr().AsSlice()
		case addressing.NodeExternalIP:
			external = address.Addr()
		}
	}
	if external.IsValid() {
		return external.AsSlice()
	}
	return nil
}
