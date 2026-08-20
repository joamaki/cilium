// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"iter"
	"maps"
	"net/netip"
	"slices"

	"github.com/cilium/cilium/pkg/node/addressing"
	"github.com/cilium/cilium/pkg/source"
)

// DataBuilder creates a modified Data value while sharing unchanged immutable
// collections with the original.
type DataBuilder struct {
	original         *Data
	data             Data
	dirty            bool
	annotationsOwned bool
}

// NewDataBuilder creates a builder initialized from data.
func NewDataBuilder(data *Data) *DataBuilder {
	if data == nil {
		panic("nil node Data")
	}
	return &DataBuilder{original: data, data: copyDataForBuilder(data)}
}

// Build returns the original Data pointer if no effective change was made.
// Otherwise it returns a new immutable snapshot. Further builder changes
// cannot modify the returned value.
func (b *DataBuilder) Build() *Data {
	result := b.Snapshot()
	b.rebase(result)
	return result
}

// Snapshot returns an immutable snapshot without changing the builder's
// original baseline. Further builder changes cannot modify the result.
func (b *DataBuilder) Snapshot() *Data {
	if !b.dirty {
		return b.original
	}
	if b.original.Equal(&b.data) {
		return b.original
	}

	out := copyDataForBuilder(&b.data)
	// The snapshot shares annotations with the builder. Clone them before the
	// next point mutation.
	b.annotationsOwned = false
	return &out
}

func (b *DataBuilder) rebase(data *Data) {
	b.original = data
	b.data = copyDataForBuilder(data)
	b.dirty = false
	b.annotationsOwned = false
}

func copyDataForBuilder(data *Data) Data {
	out := *data
	if data.local != nil {
		local := *data.local
		out.local = &local
	}
	return out
}

func (b *DataBuilder) Name() string          { return b.data.Name() }
func (b *DataBuilder) Cluster() string       { return b.data.Cluster() }
func (b *DataBuilder) ClusterID() uint32     { return b.data.ClusterID() }
func (b *DataBuilder) Source() source.Source { return b.data.Source() }
func (b *DataBuilder) Labels() iter.Seq2[string, string] {
	return b.data.Labels()
}
func (b *DataBuilder) Annotations() iter.Seq2[string, string] {
	return b.data.Annotations()
}

func (b *DataBuilder) SetIdentity(
	name, cluster string,
	clusterID uint32,
	src source.Source,
) {
	if b.data.name == name && b.data.cluster == cluster &&
		b.data.clusterID == clusterID && b.data.source == src {
		return
	}
	b.data.name = name
	b.data.cluster = cluster
	b.data.clusterID = clusterID
	b.data.source = src
	b.dirty = true
}
func (b *DataBuilder) SetClusterID(id uint32) {
	if b.data.clusterID != id {
		b.data.clusterID = id
		b.dirty = true
	}
}
func (b *DataBuilder) SetEncryptionKey(key uint8) {
	if b.data.encryptionKey != key {
		b.data.encryptionKey = key
		b.dirty = true
	}
}
func (b *DataBuilder) SetBootID(bootID string) {
	if b.data.bootID != bootID {
		b.data.bootID = bootID
		b.dirty = true
	}
}
func (b *DataBuilder) SetWireGuardPublicKey(key string) {
	if b.data.wireGuardPublicKey != key {
		b.data.wireGuardPublicKey = key
		b.dirty = true
	}
}
func (b *DataBuilder) SetAnnotation(key, value string) {
	if old, found := b.data.annotations[key]; found && old == value {
		return
	}
	b.ownAnnotations()
	b.data.annotations[key] = value
	b.dirty = true
}
func (b *DataBuilder) SetAnnotations(annotations map[string]string) {
	if maps.Equal(b.data.annotations, annotations) {
		return
	}
	b.data.annotations = maps.Clone(annotations)
	b.annotationsOwned = true
	b.dirty = true
}
func (b *DataBuilder) SetLabels(labels map[string]string) {
	if maps.Equal(b.data.labels, labels) {
		return
	}
	b.data.labels = maps.Clone(labels)
	b.dirty = true
}
func (b *DataBuilder) setLocal(info LocalNodeInfo) {
	if b.data.local != nil && *b.data.local == info {
		return
	}
	b.data.local = &info
	b.dirty = true
}
func (b *DataBuilder) UpdateLocal(update func(*LocalNodeInfo)) {
	if b.data.local == nil {
		panic("local node update called for remote node data")
	}
	old := *b.data.local
	update(b.data.local)
	if old != *b.data.local {
		b.dirty = true
	}
}

// SetAddress replaces the address for the same role and address family. An
// invalid address removes the existing address.
func (b *DataBuilder) SetAddress(
	kind AddressKind,
	nodeAddressType addressing.AddressType,
	ipv6 bool,
	addr netip.Addr,
) {
	switch kind {
	case AddressKindNode:
	case AddressKindHealth, AddressKindIngress:
		nodeAddressType = ""
	default:
		panic("SetAddress only accepts node, health, or ingress addresses")
	}
	if addr.IsValid() {
		addr = addr.Unmap()
		ipv6 = addr.Is6()
	}
	replacement := Address{Kind: kind, NodeAddressType: nodeAddressType}
	addresses := make([]Address, 0, len(b.data.addresses)+1)
	for _, address := range b.data.addresses {
		if address.Kind == kind && address.Addr().Is6() == ipv6 &&
			(kind != AddressKindNode || address.NodeAddressType == nodeAddressType) {
			continue
		}
		addresses = append(addresses, address)
	}
	if addr.IsValid() {
		replacement.Prefix = netip.PrefixFrom(addr, addr.BitLen())
		addresses = append(addresses, replacement)
	}
	addresses = sortAndCompactAddresses(addresses)
	if slices.Equal(b.data.addresses, addresses) {
		return
	}
	b.data.addresses = addresses
	b.dirty = true
}

// SetAllocationCIDRs replaces all allocation CIDRs of one address family.
func (b *DataBuilder) SetAllocationCIDRs(ipv6 bool, prefixes ...netip.Prefix) {
	addresses := make([]Address, 0, len(b.data.addresses)+len(prefixes))
	for _, address := range b.data.addresses {
		if address.Kind == AddressKindAllocation && address.Addr().Is6() == ipv6 {
			continue
		}
		addresses = append(addresses, address)
	}
	primary := true
	for _, prefix := range prefixes {
		if prefix.IsValid() && prefix.Addr().Is6() == ipv6 {
			addresses = append(addresses, Address{
				Kind: AddressKindAllocation, Prefix: prefix.Masked(), Primary: primary,
			})
			primary = false
		}
	}
	addresses = sortAndCompactAddresses(addresses)
	if slices.Equal(b.data.addresses, addresses) {
		return
	}
	b.data.addresses = addresses
	b.dirty = true
}

func (b *DataBuilder) ownAnnotations() {
	if !b.annotationsOwned {
		b.data.annotations = maps.Clone(b.data.annotations)
		b.annotationsOwned = true
	}
	if b.data.annotations == nil {
		b.data.annotations = map[string]string{}
	}
}
