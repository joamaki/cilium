// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
	"github.com/cilium/statedb/reconciler"

	"github.com/cilium/cilium/pkg/cidr"
	"github.com/cilium/cilium/pkg/container"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/source"
)

// These tables along with the [Services] API implement a new experimental internal API for
// configuring service load-balancing. The tables can be inspected with the hidden cilium-dbg
// commands "cilium-dbg statedb services" and "cilium-dbg statedb backends".
//
// These tables are reconciled with the ServiceManager to push the changes to BPF. By default
// this reconciler is disabled and it can be enabled with the hidden "enable-new-services"
// flag (by e.g. editing the cilium configmap)
const (
	BackendsTableName = "backends"
)

// Backend describes a backend for one or more services.
//
// This embeds the Backend and adds further fields to it. Eventually
// we'd unify them, but for now we do not want to affect existing code yet.
// We would rename Backend to Backend and existing Backend to BackendParams
// for similar split as with Service and ServiceParams (e.g. managed fields and user
// modifiable fields).
type Backend struct {
	loadbalancer.Backend

	// ActualState is the learned state for the backend based on health.
	ActualState loadbalancer.BackendState

	// Cluster in which the backend resides
	Cluster string

	// Source of the backend
	Source source.Source

	// ReferencedBy is the set of services referencing this backend.
	// This is managed by [Services].
	ReferencedBy container.ImmSet[loadbalancer.ServiceName]
}

func (be *Backend) String() string {
	return strings.Join(be.TableRow(), " ")
}

func (be *Backend) TableHeader() []string {
	return []string{
		"Address",
		"State",
		"Source",
		"ReferencedBy",
	}
}

func toStrings[T fmt.Stringer](xs []T) []string {
	out := make([]string, len(xs))
	for i := range xs {
		out[i] = xs[i].String()
	}
	return out
}

func (be *Backend) TableRow() []string {
	state, err := be.State.String()
	if err != nil {
		state = err.Error()
	}
	return []string{
		be.L3n4Addr.StringWithProtocol(),
		state,
		string(be.Source),
		strings.Join(toStrings(be.ReferencedBy.AsSlice()), ", "),
	}
}

func (be *Backend) InLocalCluster() bool {
	return be.Cluster == ""
}

func (be *Backend) InRemoteCluster() bool {
	return be.Cluster != ""
}

func (be *Backend) removeRef(name loadbalancer.ServiceName) (*Backend, bool) {
	beCopy := *be
	beCopy.ReferencedBy = beCopy.ReferencedBy.Delete(name)
	return &beCopy, beCopy.ReferencedBy.Len() == 0
}

// Clone returns a shallow clone of the backend.
func (be *Backend) Clone() *Backend {
	be2 := *be
	return &be2
}

var (
	BackendAddrIndex = statedb.Index[*Backend, loadbalancer.L3n4Addr]{
		Name: "addr",
		FromObject: func(obj *Backend) index.KeySet {
			return index.NewKeySet(l3n4AddrKey(obj.L3n4Addr))
		},
		FromKey: l3n4AddrKey,
		Unique:  true,
	}

	BackendServiceIndex = statedb.Index[*Backend, loadbalancer.ServiceName]{
		Name: "service-name",
		FromObject: func(obj *Backend) index.KeySet {
			return index.StringerSlice(obj.ReferencedBy.AsSlice())
		},
		FromKey: index.Stringer[loadbalancer.ServiceName],
		Unique:  false,
	}
)

func NewBackendsTable(db *statedb.DB) (statedb.RWTable[*Backend], error) {
	tbl, err := statedb.NewTable(
		BackendsTableName,
		BackendAddrIndex,
		BackendServiceIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

func GetBackendsForFrontend(txn statedb.ReadTxn, tbl statedb.Table[*Backend], svc *Frontend) statedb.Iterator[*Backend] {
	// TODO: Here we would filter out backends based on their state, and on frontend policies.
	// For now since we use ServiceManager instead of directly reconciling to BPF maps we offload that logic there.
	return tbl.List(txn, BackendServiceIndex.Query(svc.Name))
}

// FrontendParams defines a frontend. It is the only part of [Frontend] that can be updated
// outside of this package.
//
// This is currently very close to [loadbalancer.SVC], but will eventually replace it.
type FrontendParams struct {
	Name                      loadbalancer.ServiceName      // Fully qualified service name
	Source                    source.Source                 // Data source
	Address                   loadbalancer.L3n4Addr         // Frontend address
	Type                      loadbalancer.SVCType          // Service type
	ExtTrafficPolicy          loadbalancer.SVCTrafficPolicy // Service external traffic policy
	IntTrafficPolicy          loadbalancer.SVCTrafficPolicy // Service internal traffic policy
	NatPolicy                 loadbalancer.SVCNatPolicy     // Service NAT 46/64 policy
	SessionAffinity           bool
	SessionAffinityTimeoutSec uint32
	HealthCheckNodePort       uint16
	LoadBalancerSourceRanges  []*cidr.CIDR
	L7LBProxyPort             uint16 // Non-zero for L7 LB services
	LoopbackHostport          bool
}

// Clone returns a shallow clone of ServiceParams.
func (p *FrontendParams) Clone() *FrontendParams {
	p2 := *p
	return &p2
}

// Frontend describes a frontend (FrontendParams) and its state.
type Frontend struct {
	// FrontendParams describes the frontend and is the outside updatable part
	// of the frontend (rest of the fields are managed by Services).
	// This is embedded by reference to avoid copying when updating just the other fields.
	//
	// This is immutable! If you want to update the frontend you must Clone() it first!
	*FrontendParams

	// ID is the allocated numerical id for the service used in BPF
	// maps to refer to this service.
	ID loadbalancer.ID

	// BackendRevision is the backends table revision of the latest updated backend
	// that references this service. In other words, this field is updated every time
	// a backend that refers to this service updates. This allows watching the changes
	// to the service and its referenced backends by only watching the service table.
	// The reconciler for the BPF maps only watches the service and queries for the
	// backends during the reconciliation operation.
	BackendRevision statedb.Revision

	// Status is the reconciliation status of this service.
	Status reconciler.Status
}

func (s *Frontend) TableHeader() []string {
	return []string{
		"Name",
		"ID",
		"Address",
		"Type",
		"Source",
		"Status",
	}
}

func (s *Frontend) TableRow() []string {
	return []string{
		s.Name.String(),
		strconv.FormatUint(uint64(s.ID), 10),
		s.Address.StringWithProtocol(),
		string(s.Type),
		string(s.Source),
		s.Status.String(),
	}
}

// Clone returns a shallow copy of the service.
func (s *Frontend) Clone() *Frontend {
	s2 := *s
	return &s2
}

func (s *Frontend) setStatus(status reconciler.Status) *Frontend {
	s.Status = status
	return s
}

func (s *Frontend) getStatus() reconciler.Status {
	return s.Status
}

var (
	FrontendL3n4AddrIndex = statedb.Index[*Frontend, loadbalancer.L3n4Addr]{
		Name: "addr",
		FromObject: func(obj *Frontend) index.KeySet {
			return index.NewKeySet(l3n4AddrKey(obj.Address))
		},
		FromKey: l3n4AddrKey,
		Unique:  true,
	}

	FrontendNameIndex = statedb.Index[*Frontend, loadbalancer.ServiceName]{
		Name: "name",
		FromObject: func(obj *Frontend) index.KeySet {
			return index.NewKeySet(index.Stringer(obj.Name))
		},
		FromKey: index.Stringer[loadbalancer.ServiceName],
		Unique:  false,
	}
)

const (
	FrontendsTableName = "frontends"
)

func NewFrontendsTable(db *statedb.DB) (statedb.RWTable[*Frontend], error) {
	tbl, err := statedb.NewTable(
		FrontendsTableName,
		FrontendL3n4AddrIndex,
		FrontendNameIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

// l3n4AddrKey computes the StateDB key to use for L3n4Addr.
func l3n4AddrKey(addr loadbalancer.L3n4Addr) index.Key {
	return slices.Concat(
		index.NetIPAddr(addr.AddrCluster.Addr()),
		index.Uint16(addr.Port),
		index.String(addr.Protocol),
		index.Uint32(addr.AddrCluster.ClusterID()),
		[]byte{addr.Scope},
	)
}
