// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package types

import (
	"iter"
	"net/netip"

	nodedata "github.com/cilium/cilium/pkg/node/data"
	"github.com/cilium/cilium/pkg/source"
)

type kvStoreData struct{ node *KVStoreNode }

// NewKVStoreData takes ownership of n. The caller must not modify n after the
// call. The returned value exposes the stable wire object only through the
// immutable data contract.
func NewKVStoreData(n *KVStoreNode) nodedata.Data {
	if n == nil {
		panic("nil KVStoreNode")
	}
	return kvStoreData{node: n}
}

func (d kvStoreData) Name() string          { return d.node.Name }
func (d kvStoreData) Cluster() string       { return d.node.Cluster }
func (d kvStoreData) ClusterID() uint32     { return d.node.ClusterID }
func (d kvStoreData) Source() source.Source { return d.node.Source }
func (d kvStoreData) Addresses() iter.Seq[nodedata.Address] {
	return func(yield func(nodedata.Address) bool) {
		for _, address := range d.node.IPAddresses {
			addr, ok := netip.AddrFromSlice(address.IP)
			if !ok || !yield(nodedata.Address{
				Kind:            nodedata.AddressKindNode,
				NodeAddressType: address.Type,
				Prefix:          nodedata.HostPrefix(addr),
			}) {
				return
			}
		}
		for _, prefixes := range []struct {
			primary     Prefix
			secondaries []Prefix
		}{
			{d.node.IPv4AllocCIDR, d.node.IPv4SecondaryAllocCIDRs},
			{d.node.IPv6AllocCIDR, d.node.IPv6SecondaryAllocCIDRs},
		} {
			if prefixes.primary.IsValid() && !yield(nodedata.Address{
				Kind: nodedata.AddressKindAllocation, Prefix: prefixes.primary.Prefix.Prefix.Masked(), Primary: true,
			}) {
				return
			}
			for _, prefix := range prefixes.secondaries {
				if prefix.IsValid() && !yield(nodedata.Address{Kind: nodedata.AddressKindAllocation, Prefix: prefix.Prefix.Prefix.Masked()}) {
					return
				}
			}
		}
		for _, addr := range []netip.Addr{d.node.IPv4HealthIP.Addr, d.node.IPv6HealthIP.Addr} {
			if addr.IsValid() && !yield(nodedata.Address{Kind: nodedata.AddressKindHealth, Prefix: nodedata.HostPrefix(addr)}) {
				return
			}
		}
		for _, addr := range []netip.Addr{d.node.IPv4IngressIP.Addr, d.node.IPv6IngressIP.Addr} {
			if addr.IsValid() && !yield(nodedata.Address{Kind: nodedata.AddressKindIngress, Prefix: nodedata.HostPrefix(addr)}) {
				return
			}
		}
	}
}
func (d kvStoreData) EncryptionKey() uint8       { return d.node.EncryptionKey }
func (d kvStoreData) WireGuardPublicKey() string { return d.node.WireguardPubKey }
func (d kvStoreData) BootID() string             { return d.node.BootID }
func (d kvStoreData) Local() (nodedata.LocalNodeInfo, bool) {
	return nodedata.LocalNodeInfo{}, false
}
func (d kvStoreData) Label(key string) (string, bool) {
	value, found := d.node.Labels[key]
	return value, found
}
func (d kvStoreData) Labels() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for key, value := range d.node.Labels {
			if !yield(key, value) {
				return
			}
		}
	}
}
func (d kvStoreData) Annotation(key string) (string, bool) {
	value, found := d.node.Annotations[key]
	return value, found
}
func (d kvStoreData) Annotations() iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		for key, value := range d.node.Annotations {
			if !yield(key, value) {
				return
			}
		}
	}
}

var _ nodedata.Data = kvStoreData{}
