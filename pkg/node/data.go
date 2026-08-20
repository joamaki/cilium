// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"maps"
	"slices"

	nodedata "github.com/cilium/cilium/pkg/node/data"
)

type Data = nodedata.Data
type Address = nodedata.Address
type AddressKind = nodedata.AddressKind
type LocalNodeInfo = nodedata.LocalNodeInfo

const (
	AddressKindNode       = nodedata.AddressKindNode
	AddressKindAllocation = nodedata.AddressKindAllocation
	AddressKindHealth     = nodedata.AddressKindHealth
	AddressKindIngress    = nodedata.AddressKindIngress
)

// EqualData compares desired node state independent of concrete representation
// and collection iteration order.
func EqualData(a, b Data) bool {
	if a.Name() != b.Name() || a.Cluster() != b.Cluster() ||
		a.ClusterID() != b.ClusterID() || a.Source() != b.Source() ||
		a.EncryptionKey() != b.EncryptionKey() ||
		a.WireGuardPublicKey() != b.WireGuardPublicKey() ||
		a.BootID() != b.BootID() {
		return false
	}
	aa, bb := slices.Collect(a.Addresses()), slices.Collect(b.Addresses())
	slices.SortFunc(aa, Address.Compare)
	slices.SortFunc(bb, Address.Compare)
	if !slices.Equal(aa, bb) ||
		!maps.Equal(maps.Collect(a.Labels()), maps.Collect(b.Labels())) ||
		!maps.Equal(maps.Collect(a.Annotations()), maps.Collect(b.Annotations())) {
		return false
	}
	al, aLocal := a.Local()
	bl, bLocal := b.Local()
	return aLocal == bLocal && (!aLocal || al == bl)
}
