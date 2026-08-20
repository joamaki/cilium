// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/node/addressing"
	"github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
	"github.com/cilium/statedb/reconciler"
)

// Node is a Cilium node. It is the local node if [Node.Local] is non-nil.
// Node and Local are immutable once the object is inserted into the table.
//
// +deepequal-gen=false
type Node struct {
	// Data is embedded so immutable getters are available directly on Node.
	// +deepequal-gen=false
	Data

	// Statuses for reconcilers acting on this object.
	// DeepEqual is reserved for comparing the desired node data.
	// +deepequal-gen=false
	Statuses reconciler.StatusSet
}

// New constructs a node table object around immutable desired data.
func New(data Data) *Node {
	if data == nil {
		panic("nil node Data")
	}
	return &Node{Data: data}
}

// Name returns the unqualified node name.
// Fullname returns the node name qualified by its cluster when needed.
func (n *Node) Fullname() string {
	if n.Cluster() != defaults.ClusterName {
		return types.GetKeyNodeName(n.Cluster(), n.Name())
	}
	return n.Name()
}

// Identity returns the stable name and cluster identity.
func (n *Node) Identity() types.Identity {
	return types.Identity{Name: n.Name(), Cluster: n.Cluster()}
}

// IsLocal reports whether this is the local node.
func (n *Node) IsLocal() bool {
	_, local := n.Data.Local()
	return local
}

// GetNodeIP returns the preferred node IP for the requested family.
func (n *Node) GetNodeIP(ipv6 bool) net.IP {
	var fallback netip.Addr
	for address := range n.Addresses() {
		if address.Kind != AddressKindNode || address.Addr().Is6() != ipv6 {
			continue
		}
		switch address.NodeAddressType {
		case addressing.NodeInternalIP:
			return address.Addr().AsSlice()
		case addressing.NodeExternalIP:
			if !fallback.IsValid() {
				fallback = address.Addr()
			}
		default:
			if !fallback.IsValid() {
				fallback = address.Addr()
			}
		}
	}
	if fallback.IsValid() {
		return fallback.AsSlice()
	}
	return nil
}

// GetK8sNodeIP returns the preferred Kubernetes internal or external address.
func (n *Node) GetK8sNodeIP() net.IP {
	var external netip.Addr
	for address := range n.Addresses() {
		if address.Kind != AddressKindNode {
			continue
		}
		switch address.NodeAddressType {
		case addressing.NodeInternalIP:
			return address.Addr().AsSlice()
		case addressing.NodeExternalIP:
			external = address.Addr()
		}
	}
	if external.IsValid() {
		return external.AsSlice()
	}
	return nil
}

func (n *Node) IsNodeIP(addr netip.Addr) addressing.AddressType {
	for address := range n.Addresses() {
		if address.Kind == AddressKindNode && address.Addr() == addr.Unmap() {
			return address.NodeAddressType
		}
	}
	return ""
}

func (n *Node) GetCiliumInternalIP(ipv6 bool) net.IP {
	return n.nodeAddress(addressing.NodeCiliumInternalIP, ipv6)
}
func (n *Node) GetNodeInternalIPv4() net.IP { return n.nodeAddress(addressing.NodeInternalIP, false) }
func (n *Node) GetNodeInternalIPv6() net.IP { return n.nodeAddress(addressing.NodeInternalIP, true) }
func (n *Node) nodeAddress(typ addressing.AddressType, ipv6 bool) net.IP {
	for address := range n.Addresses() {
		if address.Kind == AddressKindNode && address.NodeAddressType == typ && address.Addr().Is6() == ipv6 {
			return address.Addr().AsSlice()
		}
	}
	return nil
}
func (n *Node) GetModel() *models.NodeElement { return n.ToKVStoreNode().GetModel() }

func (n *Node) GetIPv4AllocCIDRs() []netip.Prefix { return n.allocCIDRs(false) }
func (n *Node) GetIPv6AllocCIDRs() []netip.Prefix { return n.allocCIDRs(true) }
func (n *Node) allocCIDRs(ipv6 bool) []netip.Prefix {
	var out []netip.Prefix
	for address := range n.Addresses() {
		if address.Kind == AddressKindAllocation && address.Addr().Is6() == ipv6 {
			out = append(out, address.Prefix)
		}
	}
	return out
}

func (n *Node) AllocationCIDR(ipv6 bool) netip.Prefix {
	var fallback netip.Prefix
	for address := range n.Addresses() {
		if address.Kind != AddressKindAllocation || address.Addr().Is6() != ipv6 {
			continue
		}
		if address.Primary {
			return address.Prefix
		}
		if !fallback.IsValid() {
			fallback = address.Prefix
		}
	}
	return fallback
}
func (n *Node) HealthIP(ipv6 bool) netip.Addr  { return n.addressByKind(AddressKindHealth, ipv6) }
func (n *Node) IngressIP(ipv6 bool) netip.Addr { return n.addressByKind(AddressKindIngress, ipv6) }
func (n *Node) addressByKind(kind AddressKind, ipv6 bool) netip.Addr {
	for address := range n.Addresses() {
		if address.Kind == kind && address.Addr().Is6() == ipv6 {
			return address.Addr()
		}
	}
	return netip.Addr{}
}

// DeepCopy copies the table object while sharing its immutable desired data.
func (n *Node) DeepCopy() *Node {
	if n == nil {
		return nil
	}
	n2 := *n
	return &n2
}

// DeepEqual compares desired node data. Reconciliation statuses are
// deliberately excluded as they describe realized state rather than the
// desired node object.
func (n *Node) DeepEqual(other *Node) bool {
	return other != nil && EqualData(n.Data, other.Data)
}

// TableHeader implements statedb.TableWritable.
func (n *Node) TableHeader() []string {
	return []string{
		"Name",
		"Source",
		"Status",
		"Addresses",
	}
}

// TableRow implements statedb.TableWritable.
func (n *Node) TableRow() []string {
	addrs := []string{}
	for address := range n.Addresses() {
		if address.Kind == AddressKindNode {
			addrs = append(addrs, string(address.NodeAddressType)+":"+address.Addr().String())
		}
	}
	slices.Sort(addrs)
	return []string{
		n.Fullname(),
		string(n.Source()),
		n.tableStatus(),
		strings.Join(addrs, ", "),
	}
}

func (n *Node) tableStatus() string {
	statuses := n.Statuses.All()
	if len(statuses) == 0 {
		return reconciler.StatusKindPending.String()
	}

	var errors, pending, refreshing []string
	for name, status := range statuses {
		switch status.Kind {
		case reconciler.StatusKindDone:
		case reconciler.StatusKindError:
			errors = append(errors, name)
		case reconciler.StatusKindRefreshing:
			refreshing = append(refreshing, name)
		default:
			pending = append(pending, name)
		}
	}
	if len(errors)+len(pending)+len(refreshing) == 0 {
		return reconciler.StatusKindDone.String()
	}

	var parts []string
	for _, group := range []struct {
		kind  reconciler.StatusKind
		names []string
	}{
		{reconciler.StatusKindError, errors},
		{reconciler.StatusKindPending, pending},
		{reconciler.StatusKindRefreshing, refreshing},
	} {
		slices.Sort(group.names)
		if len(group.names) > 0 {
			parts = append(parts, group.kind.String()+": "+strings.Join(group.names, ","))
		}
	}
	return strings.Join(parts, "; ")
}

var _ statedb.TableWritable = &Node{}

const (
	NodeTableName = "nodes"
)

var (
	NodeNameIndex = statedb.Index[*Node, string]{
		Name: "name",
		FromObject: func(obj *Node) index.KeySet {
			return index.NewKeySet(index.String(obj.Fullname()))
		},
		FromKey:    index.String,
		FromString: index.FromString,
		Unique:     true,
	}
	NodeByName = NodeNameIndex.Query

	NodeLocalIndex = statedb.Index[*Node, bool]{
		Name: "local",
		FromObject: func(obj *Node) index.KeySet {
			if !obj.IsLocal() {
				// Don't add remote nodes to this index at all.
				return index.KeySet{}
			}
			return index.NewKeySet(index.Bool(true))
		},
		FromKey:    index.Bool,
		FromString: index.BoolString,
		Unique:     true,
	}

	NodeByLocal    = NodeLocalIndex.Query
	LocalNodeQuery = NodeByLocal(true)
)

func NewNodeTable(db *statedb.DB) (statedb.RWTable[*Node], error) {
	return statedb.NewTable(
		db,
		NodeTableName,
		NodeNameIndex,
		NodeLocalIndex,
	)
}
