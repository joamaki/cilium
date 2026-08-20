// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package v2

import (
	"net/netip"

	"github.com/cilium/cilium/pkg/annotation"
	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/source"
)

// NodeData snapshots the fields consumed by the agent into the canonical
// immutable node-table representation.
func (n *CiliumNode) NodeData(clusterInfo cmtypes.ClusterInfo) *node.Data {
	addresses := make([]node.Address, 0, len(n.Spec.Addresses)+4)
	for _, address := range n.Spec.Addresses {
		if addr, err := netip.ParseAddr(address.IP); err == nil {
			addr = addr.Unmap()
			addresses = append(addresses, node.Address{
				Kind:            node.AddressKindNode,
				NodeAddressType: address.Type,
				Prefix:          netip.PrefixFrom(addr, addr.BitLen()),
			})
		}
	}
	primaryFamily := map[bool]bool{}
	for _, prefix := range n.Spec.IPAM.PodCIDRs {
		if prefix.IsValid() {
			ipv6 := prefix.Prefix.Addr().Is6()
			addresses = append(addresses, node.Address{
				Kind: node.AddressKindAllocation, Prefix: prefix.Prefix.Masked(),
				Primary: !primaryFamily[ipv6],
			})
			primaryFamily[ipv6] = true
		}
	}
	for _, pool := range n.Spec.IPAM.Pools.Allocated {
		for _, prefix := range pool.CIDRs {
			if prefix.IsValid() {
				ipv6 := prefix.Prefix.Addr().Is6()
				addresses = append(addresses, node.Address{
					Kind: node.AddressKindAllocation, Prefix: prefix.Prefix.Masked(),
					Primary: !primaryFamily[ipv6],
				})
				primaryFamily[ipv6] = true
			}
		}
	}
	for _, raw := range []string{
		n.Spec.HealthAddressing.IPv4,
		n.Spec.HealthAddressing.IPv6,
	} {
		if addr, err := netip.ParseAddr(raw); err == nil {
			addr = addr.Unmap()
			addresses = append(addresses, node.Address{
				Kind:   node.AddressKindHealth,
				Prefix: netip.PrefixFrom(addr, addr.BitLen()),
			})
		}
	}
	for _, raw := range []string{
		n.Spec.IngressAddressing.IPV4,
		n.Spec.IngressAddressing.IPV6,
	} {
		if addr, err := netip.ParseAddr(raw); err == nil {
			addr = addr.Unmap()
			addresses = append(addresses, node.Address{
				Kind:   node.AddressKindIngress,
				Prefix: netip.PrefixFrom(addr, addr.BitLen()),
			})
		}
	}

	wireGuardKey, _ := annotation.Get(
		n,
		annotation.WireguardPubKey,
		annotation.WireguardPubKeyAlias,
	)
	return node.NewData(node.DataParams{
		Name: n.Name, Cluster: clusterInfo.Name, ClusterID: clusterInfo.ID,
		Source: source.CustomResource, Addresses: addresses,
		EncryptionKey:      uint8(n.Spec.Encryption.Key),
		WireGuardPublicKey: wireGuardKey, BootID: n.Spec.BootID,
		Labels: n.Labels, Annotations: n.Annotations,
	})
}
