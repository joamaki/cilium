// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package v2

import (
	"iter"
	"net/netip"

	"github.com/cilium/statedb/part"

	"github.com/cilium/cilium/pkg/annotation"
	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/container"
	nodedata "github.com/cilium/cilium/pkg/node/data"
	"github.com/cilium/cilium/pkg/source"
)

// ciliumNodeData is the immutable, normalized in-process representation of a
// CiliumNode. It deliberately does not translate through types.KVStoreNode.
type ciliumNodeData struct {
	name          string
	cluster       string
	clusterID     uint32
	addresses     container.ImmSet[nodedata.Address]
	encryptionKey uint8
	wireGuardKey  string
	bootID        string
	labels        part.Map[string, string]
	annotations   part.Map[string, string]
}

// CiliumNodeData snapshots the node fields consumed by the agent. Collection
// values use immutable data structures, making shallow copies of node.Node
// safe and cheap.
func (n *CiliumNode) NodeData(clusterInfo cmtypes.ClusterInfo) nodedata.Data {
	addresses := make([]nodedata.Address, 0, len(n.Spec.Addresses)+4)
	for _, address := range n.Spec.Addresses {
		if addr, err := netip.ParseAddr(address.IP); err == nil {
			addresses = append(addresses, nodedata.Address{
				Kind:            nodedata.AddressKindNode,
				NodeAddressType: address.Type,
				Prefix:          netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()),
			})
		}
	}
	primaryFamily := map[bool]bool{}
	for _, prefix := range n.Spec.IPAM.PodCIDRs {
		if prefix.IsValid() {
			ipv6 := prefix.Prefix.Addr().Is6()
			addresses = append(addresses, nodedata.Address{
				Kind: nodedata.AddressKindAllocation, Prefix: prefix.Prefix.Masked(),
				Primary: !primaryFamily[ipv6],
			})
			primaryFamily[ipv6] = true
		}
	}
	for _, pool := range n.Spec.IPAM.Pools.Allocated {
		for _, prefix := range pool.CIDRs {
			if prefix.IsValid() {
				ipv6 := prefix.Prefix.Addr().Is6()
				addresses = append(addresses, nodedata.Address{
					Kind: nodedata.AddressKindAllocation, Prefix: prefix.Prefix.Masked(),
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
			addresses = append(addresses, nodedata.Address{
				Kind: nodedata.AddressKindHealth, Prefix: netip.PrefixFrom(addr, addr.BitLen()),
			})
		}
	}
	for _, raw := range []string{
		n.Spec.IngressAddressing.IPV4,
		n.Spec.IngressAddressing.IPV6,
	} {
		if addr, err := netip.ParseAddr(raw); err == nil {
			addr = addr.Unmap()
			addresses = append(addresses, nodedata.Address{
				Kind: nodedata.AddressKindIngress, Prefix: netip.PrefixFrom(addr, addr.BitLen()),
			})
		}
	}

	wireGuardKey, _ := annotation.Get(
		n,
		annotation.WireguardPubKey,
		annotation.WireguardPubKeyAlias,
	)
	return ciliumNodeData{
		name:          n.Name,
		cluster:       clusterInfo.Name,
		clusterID:     clusterInfo.ID,
		addresses:     container.NewImmSetFunc(nodedata.Address.Compare, addresses...),
		encryptionKey: uint8(n.Spec.Encryption.Key),
		wireGuardKey:  wireGuardKey,
		bootID:        n.Spec.BootID,
		labels:        part.FromMap(part.Map[string, string]{}, n.Labels),
		annotations:   part.FromMap(part.Map[string, string]{}, n.Annotations),
	}
}

func (d ciliumNodeData) Name() string          { return d.name }
func (d ciliumNodeData) Cluster() string       { return d.cluster }
func (d ciliumNodeData) ClusterID() uint32     { return d.clusterID }
func (d ciliumNodeData) Source() source.Source { return source.CustomResource }
func (d ciliumNodeData) Addresses() iter.Seq[nodedata.Address] {
	return func(yield func(nodedata.Address) bool) {
		for _, address := range d.addresses.AsSlice() {
			if !yield(address) {
				return
			}
		}
	}
}
func (d ciliumNodeData) EncryptionKey() uint8       { return d.encryptionKey }
func (d ciliumNodeData) WireGuardPublicKey() string { return d.wireGuardKey }
func (d ciliumNodeData) BootID() string             { return d.bootID }
func (d ciliumNodeData) Label(key string) (string, bool) {
	return d.labels.Get(key)
}
func (d ciliumNodeData) Labels() iter.Seq2[string, string] { return d.labels.All() }
func (d ciliumNodeData) Annotation(key string) (string, bool) {
	return d.annotations.Get(key)
}
func (d ciliumNodeData) Annotations() iter.Seq2[string, string] {
	return d.annotations.All()
}
func (d ciliumNodeData) Local() (nodedata.LocalNodeInfo, bool) {
	return nodedata.LocalNodeInfo{}, false
}

var _ nodedata.Data = ciliumNodeData{}
