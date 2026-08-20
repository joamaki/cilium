// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

// Package data defines the immutable desired-state contract shared by node
// producers and the in-process node table.
package data

import (
	"cmp"
	"iter"
	"net/netip"

	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/cilium/cilium/pkg/datapath/tunnel"
	"github.com/cilium/cilium/pkg/node/addressing"
	"github.com/cilium/cilium/pkg/source"
)

// Data is the immutable desired state of a node. Implementations must not
// expose mutable maps, slices or pointers through this interface.
type Data interface {
	Name() string
	Cluster() string
	ClusterID() uint32
	Source() source.Source
	Addresses() iter.Seq[Address]
	EncryptionKey() uint8
	WireGuardPublicKey() string
	BootID() string
	Label(string) (string, bool)
	Labels() iter.Seq2[string, string]
	Annotation(string) (string, bool)
	Annotations() iter.Seq2[string, string]
	Local() (LocalNodeInfo, bool)
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

func HostPrefix(addr netip.Addr) netip.Prefix {
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen())
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
