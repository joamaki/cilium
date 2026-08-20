// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"encoding/json"
	"maps"
	"net"
	"net/netip"

	"github.com/cilium/cilium/pkg/ip"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
)

// FromKVStoreNode wraps a transferred KVStore node in the immutable table
// representation.
func FromKVStoreNode(n *nodeTypes.KVStoreNode) *Node {
	return New(nodeTypes.NewKVStoreData(n))
}

// ToKVStoreNode materializes the stable KVStore/ClusterMesh representation.
// This is intentionally an explicit boundary: in-process node state uses the
// immutable Data interface and unified addresses instead.
func (n *Node) ToKVStoreNode() *nodeTypes.KVStoreNode {
	out := &nodeTypes.KVStoreNode{
		Name: n.Name(), Cluster: n.Cluster(), ClusterID: n.ClusterID(),
		Source: n.Source(), EncryptionKey: n.EncryptionKey(),
		WireguardPubKey: n.WireGuardPublicKey(), BootID: n.BootID(),
		Labels: maps.Collect(n.Labels()), Annotations: maps.Collect(n.Annotations()),
	}
	var ipv4Primary, ipv6Primary nodeTypes.Prefix
	var ipv4Secondary, ipv6Secondary []nodeTypes.Prefix
	for address := range n.Addresses() {
		switch address.Kind {
		case AddressKindNode:
			out.IPAddresses = append(out.IPAddresses, nodeTypes.Address{
				Type: address.NodeAddressType, IP: net.IP(address.Addr().AsSlice()),
			})
		case AddressKindAllocation:
			prefix := nodeTypes.PrefixFrom(address.Prefix)
			if address.Addr().Is4() {
				if address.Primary && !ipv4Primary.IsValid() {
					ipv4Primary = prefix
				} else {
					ipv4Secondary = append(ipv4Secondary, prefix)
				}
			} else if address.Primary && !ipv6Primary.IsValid() {
				ipv6Primary = prefix
			} else {
				ipv6Secondary = append(ipv6Secondary, prefix)
			}
		case AddressKindHealth:
			setFamilyAddr(address.Addr(), &out.IPv4HealthIP, &out.IPv6HealthIP)
		case AddressKindIngress:
			setFamilyAddr(address.Addr(), &out.IPv4IngressIP, &out.IPv6IngressIP)
		}
	}
	out.IPv4AllocCIDR, out.IPv4SecondaryAllocCIDRs =
		allocationCIDRs(ipv4Primary, ipv4Secondary)
	out.IPv6AllocCIDR, out.IPv6SecondaryAllocCIDRs =
		allocationCIDRs(ipv6Primary, ipv6Secondary)
	return out
}

func allocationCIDRs(
	primary nodeTypes.Prefix,
	secondary []nodeTypes.Prefix,
) (nodeTypes.Prefix, []nodeTypes.Prefix) {
	// Data implementations should identify the primary allocation CIDR. Retain
	// compatibility with implementations that only provide an unordered set by
	// promoting the first CIDR when none is explicitly primary.
	if !primary.IsValid() && len(secondary) > 0 {
		primary, secondary = secondary[0], secondary[1:]
	}
	return primary, secondary
}

func setFamilyAddr(addr netip.Addr, ipv4, ipv6 *ip.Addr) {
	if addr.Is4() {
		*ipv4 = ip.AddrFrom(addr)
	} else {
		*ipv6 = ip.AddrFrom(addr)
	}
}

func (n *Node) MarshalJSON() ([]byte, error) { return json.Marshal(n.ToKVStoreNode()) }
func (n *Node) MarshalYAML() (any, error)    { return n.ToKVStoreNode(), nil }
