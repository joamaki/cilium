// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"iter"
	"maps"
	"net/netip"

	"github.com/cilium/statedb/part"

	"github.com/cilium/cilium/pkg/container"
	"github.com/cilium/cilium/pkg/node/addressing"
	"github.com/cilium/cilium/pkg/source"
)

type localData struct {
	name, cluster              string
	clusterID                  uint32
	source                     source.Source
	addresses                  container.ImmSet[Address]
	encryptionKey              uint8
	wireGuardPublicKey, bootID string
	labels, annotations        part.Map[string, string]
	local                      LocalNodeInfo
}

// LocalNodeMutator is a transaction-scoped editor for the local node. It is
// created by LocalNodeStore and publishes an immutable localData value after
// the update callback returns.
type LocalNodeMutator struct {
	data localData
}

// NewLocalData snapshots desired node data into the optimized local
// representation and attaches local-only information.
func NewLocalData(d Data, info LocalNodeInfo) Data {
	addresses := make([]Address, 0)
	for address := range d.Addresses() {
		addresses = append(addresses, address)
	}
	return localData{
		name: d.Name(), cluster: d.Cluster(), clusterID: d.ClusterID(), source: d.Source(),
		addresses:          container.NewImmSetFunc(Address.Compare, addresses...),
		encryptionKey:      d.EncryptionKey(),
		wireGuardPublicKey: d.WireGuardPublicKey(),
		bootID:             d.BootID(),
		labels:             part.FromMap(part.Map[string, string]{}, maps.Collect(d.Labels())),
		annotations:        part.FromMap(part.Map[string, string]{}, maps.Collect(d.Annotations())),
		local:              info,
	}
}

func (d localData) Name() string          { return d.name }
func (d localData) Cluster() string       { return d.cluster }
func (d localData) ClusterID() uint32     { return d.clusterID }
func (d localData) Source() source.Source { return d.source }
func (d localData) Addresses() iter.Seq[Address] {
	return func(yield func(Address) bool) {
		for _, a := range d.addresses.AsSlice() {
			if !yield(a) {
				return
			}
		}
	}
}
func (d localData) EncryptionKey() uint8                   { return d.encryptionKey }
func (d localData) WireGuardPublicKey() string             { return d.wireGuardPublicKey }
func (d localData) BootID() string                         { return d.bootID }
func (d localData) Label(k string) (string, bool)          { return d.labels.Get(k) }
func (d localData) Labels() iter.Seq2[string, string]      { return d.labels.All() }
func (d localData) Annotation(k string) (string, bool)     { return d.annotations.Get(k) }
func (d localData) Annotations() iter.Seq2[string, string] { return d.annotations.All() }
func (d localData) Local() (LocalNodeInfo, bool)           { return d.local, true }

var _ Data = localData{}

func newLocalNodeMutator(d Data) *LocalNodeMutator {
	return &LocalNodeMutator{data: asLocalData(d)}
}

func (m *LocalNodeMutator) Name() string          { return m.data.Name() }
func (m *LocalNodeMutator) Cluster() string       { return m.data.Cluster() }
func (m *LocalNodeMutator) ClusterID() uint32     { return m.data.ClusterID() }
func (m *LocalNodeMutator) Source() source.Source { return m.data.Source() }
func (m *LocalNodeMutator) Labels() iter.Seq2[string, string] {
	return m.data.Labels()
}
func (m *LocalNodeMutator) Annotations() iter.Seq2[string, string] {
	return m.data.Annotations()
}

// Node returns an immutable snapshot of the edits made so far.
func (m *LocalNodeMutator) Node() *Node { return New(m.data) }

func (m *LocalNodeMutator) SetIdentity(name, cluster string, clusterID uint32, src source.Source) {
	m.data.name = name
	m.data.cluster = cluster
	m.data.clusterID = clusterID
	m.data.source = src
}

func (m *LocalNodeMutator) SetClusterID(id uint32) { m.data.clusterID = id }
func (m *LocalNodeMutator) SetEncryptionKey(key uint8) {
	m.data.encryptionKey = key
}
func (m *LocalNodeMutator) SetBootID(bootID string) { m.data.bootID = bootID }
func (m *LocalNodeMutator) SetWireGuardPublicKey(key string) {
	m.data.wireGuardPublicKey = key
}
func (m *LocalNodeMutator) SetAnnotation(key, value string) {
	m.data.annotations = m.data.annotations.Set(key, value)
}
func (m *LocalNodeMutator) SetAnnotations(annotations map[string]string) {
	m.data.annotations = part.FromMap(part.Map[string, string]{}, maps.Clone(annotations))
}
func (m *LocalNodeMutator) SetLabels(labels map[string]string) {
	m.data.labels = part.FromMap(part.Map[string, string]{}, maps.Clone(labels))
}
func (m *LocalNodeMutator) UpdateLocalInfo(update func(*LocalNodeInfo)) {
	update(&m.data.local)
}

// SetAddress replaces the address for the same role and address family. An
// invalid address removes the existing address.
func (m *LocalNodeMutator) SetAddress(
	kind AddressKind,
	nodeAddressType addressing.AddressType,
	ipv6 bool,
	addr netip.Addr,
) {
	replacement := Address{Kind: kind, NodeAddressType: nodeAddressType}
	for _, address := range m.data.addresses.AsSlice() {
		if address.Kind == kind && address.Addr().Is6() == ipv6 &&
			(kind != AddressKindNode || address.NodeAddressType == nodeAddressType) {
			m.data.addresses = m.data.addresses.Delete(address)
		}
	}
	if addr.IsValid() {
		addr = addr.Unmap()
		replacement.Prefix = netip.PrefixFrom(addr, addr.BitLen())
		m.data.addresses = m.data.addresses.Insert(replacement)
	}
}

// SetAllocationCIDRs replaces all allocation CIDRs of one address family.
func (m *LocalNodeMutator) SetAllocationCIDRs(ipv6 bool, prefixes ...netip.Prefix) {
	for _, address := range m.data.addresses.AsSlice() {
		if address.Kind == AddressKindAllocation && address.Addr().Is6() == ipv6 {
			m.data.addresses = m.data.addresses.Delete(address)
		}
	}
	for i, prefix := range prefixes {
		if prefix.IsValid() && prefix.Addr().Is6() == ipv6 {
			m.data.addresses = m.data.addresses.Insert(Address{
				Kind: AddressKindAllocation, Prefix: prefix.Masked(), Primary: i == 0,
			})
		}
	}
}

func asLocalData(d Data) localData {
	local, ok := d.(localData)
	if !ok {
		panic("local node update called for non-local node data")
	}
	return local
}
