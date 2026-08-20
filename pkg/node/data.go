// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"cmp"
	"iter"
	"maps"
	"net/netip"
	"slices"

	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/cilium/cilium/pkg/datapath/tunnel"
	"github.com/cilium/cilium/pkg/node/addressing"
	"github.com/cilium/cilium/pkg/source"
)

// Data is the canonical immutable desired state of a node.
//
// Collection fields are not exposed directly. A Data value can therefore be
// shared by table object copies; DataBuilder clones collections before
// modifying them.
type Data struct {
	name, cluster              string
	clusterID                  uint32
	source                     source.Source
	addresses                  []Address
	encryptionKey              uint8
	wireGuardPublicKey, bootID string
	labels, annotations        map[string]string
	local                      *LocalNodeInfo
}

// DataParams contains the normalized values used to construct Data. NewData
// clones all mutable inputs.
type DataParams struct {
	Name               string
	Cluster            string
	ClusterID          uint32
	Source             source.Source
	Addresses          []Address
	EncryptionKey      uint8
	WireGuardPublicKey string
	BootID             string
	Labels             map[string]string
	Annotations        map[string]string
}

// NewData constructs immutable normalized node data.
func NewData(p DataParams) *Data {
	d := &Data{
		name: p.Name, cluster: p.Cluster, clusterID: p.ClusterID,
		source:             p.Source,
		addresses:          normalizeAddresses(p.Addresses),
		encryptionKey:      p.EncryptionKey,
		wireGuardPublicKey: p.WireGuardPublicKey,
		bootID:             p.BootID,
		labels:             maps.Clone(p.Labels),
		annotations:        maps.Clone(p.Annotations),
	}
	return d
}

func (d *Data) Name() string          { return d.name }
func (d *Data) Cluster() string       { return d.cluster }
func (d *Data) ClusterID() uint32     { return d.clusterID }
func (d *Data) Source() source.Source { return d.source }
func (d *Data) Addresses() iter.Seq[Address] {
	return slices.Values(d.addresses)
}
func (d *Data) EncryptionKey() uint8              { return d.encryptionKey }
func (d *Data) WireGuardPublicKey() string        { return d.wireGuardPublicKey }
func (d *Data) BootID() string                    { return d.bootID }
func (d *Data) Label(key string) (string, bool)   { value, ok := d.labels[key]; return value, ok }
func (d *Data) Labels() iter.Seq2[string, string] { return maps.All(d.labels) }
func (d *Data) Annotation(key string) (string, bool) {
	value, ok := d.annotations[key]
	return value, ok
}
func (d *Data) Annotations() iter.Seq2[string, string] { return maps.All(d.annotations) }
func (d *Data) Local() (LocalNodeInfo, bool) {
	if d.local == nil {
		return LocalNodeInfo{}, false
	}
	return *d.local, true
}

// Equal reports whether two values describe the same desired node state.
func (d *Data) Equal(other *Data) bool {
	if d == other {
		return true
	}
	if d == nil || other == nil ||
		d.name != other.name || d.cluster != other.cluster ||
		d.clusterID != other.clusterID || d.source != other.source ||
		d.encryptionKey != other.encryptionKey ||
		d.wireGuardPublicKey != other.wireGuardPublicKey ||
		d.bootID != other.bootID ||
		!slices.Equal(d.addresses, other.addresses) ||
		!maps.Equal(d.labels, other.labels) ||
		!maps.Equal(d.annotations, other.annotations) ||
		(d.local == nil) != (other.local == nil) {
		return false
	}
	return d.local == nil || *d.local == *other.local
}

// EqualData compares canonical desired node state.
func EqualData(a, b *Data) bool { return a.Equal(b) }

func normalizeAddresses(addresses []Address) []Address {
	normalized := make([]Address, 0, len(addresses))
	var (
		firstAllocation    [2]netip.Prefix
		explicitAllocation [2]netip.Prefix
	)
	for _, address := range addresses {
		if !address.Prefix.IsValid() {
			continue
		}
		switch address.Kind {
		case AddressKindNode:
			addr := address.Addr().Unmap()
			address.Prefix = netip.PrefixFrom(addr, addr.BitLen())
			address.Primary = false
		case AddressKindAllocation:
			address.Prefix = address.Prefix.Masked()
			address.NodeAddressType = ""
			family := addressFamily(address.Addr())
			if !firstAllocation[family].IsValid() {
				firstAllocation[family] = address.Prefix
			}
			if address.Primary && !explicitAllocation[family].IsValid() {
				explicitAllocation[family] = address.Prefix
			}
		case AddressKindHealth, AddressKindIngress:
			addr := address.Addr().Unmap()
			address.Prefix = netip.PrefixFrom(addr, addr.BitLen())
			address.NodeAddressType = ""
			address.Primary = false
		default:
			continue
		}
		normalized = append(normalized, address)
	}
	for i := range normalized {
		address := &normalized[i]
		if address.Kind != AddressKindAllocation {
			continue
		}
		family := addressFamily(address.Addr())
		primary := explicitAllocation[family]
		if !primary.IsValid() {
			primary = firstAllocation[family]
		}
		address.Primary = address.Prefix == primary
	}
	return sortAndCompactAddresses(normalized)
}

func addressFamily(addr netip.Addr) int {
	if addr.Is6() {
		return 1
	}
	return 0
}

func sortAndCompactAddresses(addresses []Address) []Address {
	slices.SortFunc(addresses, Address.Compare)
	return slices.CompactFunc(addresses, func(a, b Address) bool {
		return a.Compare(b) == 0
	})
}

type AddressKind uint8

const (
	AddressKindNode AddressKind = iota
	AddressKindAllocation
	AddressKindHealth
	AddressKindIngress
)

// Address represents both host addresses (/32 or /128) and allocation ranges.
type Address struct {
	Kind            AddressKind
	NodeAddressType addressing.AddressType
	Prefix          netip.Prefix
	// Primary distinguishes the allocation CIDR published in the stable node
	// format from any secondary allocation CIDRs.
	Primary bool
}

func (a Address) Addr() netip.Addr { return a.Prefix.Addr() }

func (a Address) Compare(b Address) int {
	if n := cmp.Compare(a.Kind, b.Kind); n != 0 {
		return n
	}
	if n := cmp.Compare(a.NodeAddressType, b.NodeAddressType); n != 0 {
		return n
	}
	if a.Primary != b.Primary {
		if a.Primary {
			return -1
		}
		return 1
	}
	return a.Prefix.Compare(b.Prefix)
}

// LocalNodeInfo is desired state that exists only for the local node.
type LocalNodeInfo struct {
	OptOutNodeEncryption  bool
	UID                   k8stypes.UID
	ProviderID            string
	IPv4NativeRoutingCIDR netip.Prefix
	IPv6NativeRoutingCIDR netip.Prefix
	ServiceLoopbackIPv4   netip.Addr
	ServiceLoopbackIPv6   netip.Addr
	IsBeingDeleted        bool
	UnderlayProtocol      tunnel.UnderlayProtocol
}

func (in *LocalNodeInfo) DeepCopyInto(out *LocalNodeInfo) { *out = *in }
func (in *LocalNodeInfo) DeepCopy() *LocalNodeInfo {
	if in == nil {
		return nil
	}
	out := new(LocalNodeInfo)
	in.DeepCopyInto(out)
	return out
}
func (in *LocalNodeInfo) DeepEqual(other *LocalNodeInfo) bool {
	return other != nil && *in == *other
}
