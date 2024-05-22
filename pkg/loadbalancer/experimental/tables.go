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
	"github.com/cilium/statedb/part"
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

	Props Props

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

type Prop interface {
	fmt.Stringer
}

// Props is a schemaless set of information associated with a service, frontend or backend
// that can be used to carry feature-specific information related to load-balancing. Its main
// use-case is as an extension mechanism for projects that extend Cilium.
type Props = part.Map[string, Prop]

func NewProps() Props {
	return part.NewStringMap[Prop]()
}

func ShowProps(p Props) string {
	var b strings.Builder
	iter := p.All()
	k, v, ok := iter.Next()
	for ok {
		b.WriteString(k)
		b.WriteRune('=')
		b.WriteString(v.String())
		k, v, ok = iter.Next()
		if ok {
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// Service defines the common properties for a load-balancing service. Associated with a
// service are a set of frontends that receive the traffic, and a set of backends to which
// the traffic is directed. A single frontend can map to a partial subset of backends depending
// on its properties.
type Service struct {
	Name                      loadbalancer.ServiceName // Fully qualified service name
	Source                    source.Source            // Data source
	Props                     Props
	Labels                    map[string]string
	Selector                  map[string]string
	Annotations               map[string]string
	ExtTrafficPolicy          loadbalancer.SVCTrafficPolicy // Service external traffic policy
	IntTrafficPolicy          loadbalancer.SVCTrafficPolicy // Service internal traffic policy
	NatPolicy                 loadbalancer.SVCNatPolicy     // Service NAT 46/64 policy
	SessionAffinity           bool
	SessionAffinityTimeoutSec uint32
	HealthCheckNodePort       uint16
	LoadBalancerSourceRanges  container.ImmSet[*cidr.CIDR]
	LoopbackHostport          bool

	// from k8s.Service
	IncludeExternal bool
	Shared          bool
	ServiceAffinity string // TODO define new type

	// L7ProxyPort if non-zero specifies to which L7 proxy to redirect the frontend
	// traffic.
	L7ProxyPort uint16

	// L7Ports specifies by port which frontends should be redirected to L7 proxy.
	// If empty then all are redirected.
	L7Ports container.ImmSet[uint16]
}

// FrontendParams defines a frontend. It is the only part of [Frontend] that can be updated
// outside of this package.
type FrontendParams struct {
	Name    loadbalancer.ServiceName // Fully qualified service name
	Source  source.Source            // Data source
	Address loadbalancer.L3n4Addr    // Frontend address
	Type    loadbalancer.SVCType     // Service type
	Props   Props
}

// Clone returns a shallow clone of ServiceParams.
func (p *FrontendParams) Clone() *FrontendParams {
	p2 := *p
	return &p2
}

// Frontend describes a frontend (FrontendParams) and its state.
// This object is the primary object reconciled towards the BPF maps, with the
// [Service] and [Backend] being queried during reconcilation. The revision fields
// create the link between them.
type Frontend struct {
	// FrontendParams describes the frontend and is the outside updatable part
	// of the frontend (rest of the fields are managed by Services).
	// This is embedded by reference to avoid copying when updating just the other fields.
	//
	// This is immutable! If you want to update the frontend you must Clone() it first!
	*FrontendParams

	ServiceRevision statedb.Revision

	// BackendsRevision is the backends table revision of the latest updated backend
	// that references this service.
	BackendsRevision statedb.Revision

	// ID is the allocated numerical id for the service used in BPF
	// maps to refer to this service.
	ID loadbalancer.ID

	// Status is the reconciliation status of this service.
	Status reconciler.Status
}

func (fe *Frontend) TableHeader() []string {
	return []string{
		"Name",
		"ID",
		"Address",
		"Type",
		"Source",
		"Props",
		"Status",
	}
}

func (fe *Frontend) TableRow() []string {
	return []string{
		fe.Name.String(),
		strconv.FormatUint(uint64(fe.ID), 10),
		fe.Address.StringWithProtocol(),
		string(fe.Type),
		string(fe.Source),
		ShowProps(fe.Props),
		fe.Status.String(),
	}
}

// Clone returns a shallow copy of the service.
func (fe *Frontend) Clone() *Frontend {
	fe2 := *fe
	return &fe2
}

func (fe *Frontend) SetProp(key string, value Prop) *Frontend {
	fe = fe.Clone()
	fe.Props = fe.Props.Set(key, value)
	return fe
}

func (fe *Frontend) setStatus(status reconciler.Status) *Frontend {
	fe.Status = status
	return fe
}

func (fe *Frontend) getStatus() reconciler.Status {
	return fe.Status
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
