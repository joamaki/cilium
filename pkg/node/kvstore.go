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
	return New(DataFromKVStoreNode(n))
}

// DataFromKVStoreNode snapshots the stable KVStore representation into
// canonical immutable node data.
func DataFromKVStoreNode(n *nodeTypes.KVStoreNode) *Data {
	if n == nil {
		panic("nil KVStoreNode")
	}
	addresses := make([]Address, 0, len(n.IPAddresses)+8)
	for _, address := range n.IPAddresses {
		if addr, ok := netip.AddrFromSlice(address.IP); ok {
			addresses = append(addresses, Address{
				Kind:            AddressKindNode,
				NodeAddressType: address.Type,
				Prefix:          nodeHostPrefix(addr),
			})
		}
	}
	for _, prefixes := range []struct {
		primary     nodeTypes.Prefix
		secondaries []nodeTypes.Prefix
	}{
		{n.IPv4AllocCIDR, n.IPv4SecondaryAllocCIDRs},
		{n.IPv6AllocCIDR, n.IPv6SecondaryAllocCIDRs},
	} {
		if prefixes.primary.IsValid() {
			addresses = append(addresses, Address{
				Kind:   AddressKindAllocation,
				Prefix: prefixes.primary.Prefix.Prefix.Masked(), Primary: true,
			})
		}
		for _, prefix := range prefixes.secondaries {
			if prefix.IsValid() {
				addresses = append(addresses, Address{
					Kind: AddressKindAllocation, Prefix: prefix.Prefix.Prefix.Masked(),
				})
			}
		}
	}
	for _, addr := range []netip.Addr{n.IPv4HealthIP.Addr, n.IPv6HealthIP.Addr} {
		if addr.IsValid() {
			addresses = append(addresses, Address{
				Kind: AddressKindHealth, Prefix: nodeHostPrefix(addr),
			})
		}
	}
	for _, addr := range []netip.Addr{n.IPv4IngressIP.Addr, n.IPv6IngressIP.Addr} {
		if addr.IsValid() {
			addresses = append(addresses, Address{
				Kind: AddressKindIngress, Prefix: nodeHostPrefix(addr),
			})
		}
	}
	return NewData(DataParams{
		Name: n.Name, Cluster: n.Cluster, ClusterID: n.ClusterID,
		Source: n.Source, Addresses: addresses,
		EncryptionKey: n.EncryptionKey, WireGuardPublicKey: n.WireguardPubKey,
		BootID: n.BootID, Labels: n.Labels, Annotations: n.Annotations,
	})
}

func nodeHostPrefix(addr netip.Addr) netip.Prefix {
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen())
}

// ToKVStoreNode materializes the stable KVStore/ClusterMesh representation.
// This is intentionally an explicit boundary: in-process node state uses the
// canonical immutable Data representation and unified addresses instead.
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
	// Canonical data identifies the primary allocation CIDR. Retain a defensive
	// fallback for malformed inputs by promoting the first CIDR when needed.
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
