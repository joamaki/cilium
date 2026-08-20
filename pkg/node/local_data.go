// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"iter"
	"net/netip"

	"github.com/cilium/cilium/pkg/node/addressing"
	"github.com/cilium/cilium/pkg/source"
)

// LocalNodeMutator is a transaction-scoped editor for the local node. It is
// created by LocalNodeStore and publishes an immutable Data snapshot after
// the update callback returns.
type LocalNodeMutator struct {
	builder *DataBuilder
}

// NewLocalData attaches local-only information to canonical node data.
func NewLocalData(data *Data, info LocalNodeInfo) *Data {
	builder := NewDataBuilder(data)
	builder.setLocal(info)
	return builder.Build()
}

func newLocalNodeMutator(data *Data) *LocalNodeMutator {
	if _, local := data.Local(); !local {
		panic("local node update called for remote node data")
	}
	return &LocalNodeMutator{builder: NewDataBuilder(data)}
}

func (m *LocalNodeMutator) build() *Data { return m.builder.Build() }

func (m *LocalNodeMutator) Name() string          { return m.builder.Name() }
func (m *LocalNodeMutator) Cluster() string       { return m.builder.Cluster() }
func (m *LocalNodeMutator) ClusterID() uint32     { return m.builder.ClusterID() }
func (m *LocalNodeMutator) Source() source.Source { return m.builder.Source() }
func (m *LocalNodeMutator) Labels() iter.Seq2[string, string] {
	return m.builder.Labels()
}
func (m *LocalNodeMutator) Annotations() iter.Seq2[string, string] {
	return m.builder.Annotations()
}

// Node returns an immutable snapshot of the edits made so far.
func (m *LocalNodeMutator) Node() *Node { return New(m.builder.Snapshot()) }

func (m *LocalNodeMutator) SetIdentity(
	name, cluster string,
	clusterID uint32,
	src source.Source,
) {
	m.builder.SetIdentity(name, cluster, clusterID, src)
}
func (m *LocalNodeMutator) SetClusterID(id uint32) {
	m.builder.SetClusterID(id)
}
func (m *LocalNodeMutator) SetEncryptionKey(key uint8) {
	m.builder.SetEncryptionKey(key)
}
func (m *LocalNodeMutator) SetBootID(bootID string) {
	m.builder.SetBootID(bootID)
}
func (m *LocalNodeMutator) SetWireGuardPublicKey(key string) {
	m.builder.SetWireGuardPublicKey(key)
}
func (m *LocalNodeMutator) SetAnnotation(key, value string) {
	m.builder.SetAnnotation(key, value)
}
func (m *LocalNodeMutator) SetAnnotations(annotations map[string]string) {
	m.builder.SetAnnotations(annotations)
}
func (m *LocalNodeMutator) SetLabels(labels map[string]string) {
	m.builder.SetLabels(labels)
}
func (m *LocalNodeMutator) UpdateLocalInfo(update func(*LocalNodeInfo)) {
	m.builder.UpdateLocal(update)
}
func (m *LocalNodeMutator) SetAddress(
	kind AddressKind,
	nodeAddressType addressing.AddressType,
	ipv6 bool,
	addr netip.Addr,
) {
	m.builder.SetAddress(kind, nodeAddressType, ipv6, addr)
}
func (m *LocalNodeMutator) SetAllocationCIDRs(
	ipv6 bool,
	prefixes ...netip.Prefix,
) {
	m.builder.SetAllocationCIDRs(ipv6, prefixes...)
}
