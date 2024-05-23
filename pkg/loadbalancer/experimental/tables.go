// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
	"github.com/cilium/statedb/part"
	"github.com/cilium/statedb/reconciler"

	"github.com/cilium/cilium/pkg/cidr"
	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/container"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/source"
)

// These tables along with the [Services] API implement a new experimental internal API for
// configuring service load-balancing. The tables can be inspected with the hidden cilium-dbg
// commands "cilium-dbg statedb services" and "cilium-dbg statedb backends".
//
// The tables are reconciled with the ServiceManager to push the changes to BPF. By default
// this reconciler is disabled and it can be enabled with the hidden "enable-new-services"
// flag (by e.g. editing the cilium configmap)
const (
	BackendTableName = "backends"
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
		BackendTableName,
		BackendAddrIndex,
		BackendServiceIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

func GetBackendsForService(txn statedb.ReadTxn, tbl statedb.Table[*Backend], svc *Service) statedb.Iterator[*Backend] {
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

var EmptyProps = NewProps()

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
	Labels                    map[string]string             // Immutable
	Selector                  map[string]string             // Immutable
	Annotations               map[string]string             // Immutable
	ExtTrafficPolicy          loadbalancer.SVCTrafficPolicy // Service external traffic policy
	IntTrafficPolicy          loadbalancer.SVCTrafficPolicy // Service internal traffic policy
	NatPolicy                 loadbalancer.SVCNatPolicy     // Service NAT 46/64 policy
	SessionAffinity           bool
	SessionAffinityTimeoutSec uint32
	HealthCheckNodePort       uint16
	LoadBalancerSourceRanges  []*cidr.CIDR
	LoopbackHostport          bool

	// from k8s.Service
	IncludeExternal bool
	Shared          bool
	ServiceAffinity string // TODO define new type

	// L7ProxyPort if non-zero specifies to which L7 proxy to redirect the frontend
	// traffic.
	L7ProxyPort uint16

	// L7FrontendPorts specifies by port which frontends should be redirected to L7 proxy.
	// If empty then all are redirected.
	L7FrontendPorts container.ImmSet[uint16]

	Frontends part.Map[loadbalancer.L3n4Addr, Frontend]

	// BackendsRevision is the backends table revision of the latest updated backend
	// that references this service.
	BackendsRevision statedb.Revision

	Status reconciler.Status
}

func (svc *Service) Clone() *Service {
	svc2 := *svc
	return &svc2
}

func (svc *Service) setStatus(status reconciler.Status) *Service {
	svc.Status = status
	return svc
}

func (svc *Service) getStatus() reconciler.Status {
	return svc.Status
}

func (svc *Service) TableHeader() []string {
	return []string{
		"Name",
		"Source",
		"Props",
		"Frontends",
		"Status",
	}
}

func (svc *Service) TableRow() []string {
	return []string{
		svc.Name.String(),
		string(svc.Source),
		ShowProps(svc.Props),
		ShowFrontends(svc.Frontends),
		svc.Status.String(),
	}
}

var (
	ServiceNameIndex = statedb.Index[*Service, loadbalancer.ServiceName]{
		Name: "name",
		FromObject: func(obj *Service) index.KeySet {
			return index.NewKeySet(index.Stringer(obj.Name))
		},
		FromKey: index.Stringer[loadbalancer.ServiceName],
		Unique:  true,
	}

	ServiceFrontendsIndex = statedb.Index[*Service, loadbalancer.L3n4Addr]{
		Name: "frontends",
		FromObject: func(obj *Service) index.KeySet {
			var keys []index.Key
			iter := obj.Frontends.All()
			for addr, _, ok := iter.Next(); ok; addr, _, ok = iter.Next() {
				keys = append(keys, l3n4AddrKey(addr))
			}
			return index.NewKeySet(keys...)
		},
		FromKey: l3n4AddrKey,
		Unique:  true,
	}
)

const (
	ServiceTableName = "services"
)

func NewServicesTable(db *statedb.DB) (statedb.RWTable[*Service], error) {
	tbl, err := statedb.NewTable(
		ServiceTableName,
		ServiceNameIndex,
		ServiceFrontendsIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

type Frontend struct {
	Address  loadbalancer.L3n4Addr // Frontend address
	Type     loadbalancer.SVCType  // Service type
	PortName string
	Props    Props

	// ID is the allocated numerical id for the frontend used in BPF
	// maps to refer to this service.
	ID loadbalancer.ID
}

func NewFrontendsMap(fes ...Frontend) part.Map[loadbalancer.L3n4Addr, Frontend] {
	m := part.NewMap[loadbalancer.L3n4Addr, Frontend](
		func(addr loadbalancer.L3n4Addr) []byte { return []byte(l3n4AddrKey(addr)) },
		l3n4AddrFromBytes,
	)
	for _, fe := range fes {
		m = m.Set(fe.Address, fe)
	}
	return m
}

var EmptyFrontendsMap = NewFrontendsMap()

func ShowFrontends(m part.Map[loadbalancer.L3n4Addr, Frontend]) string {
	var b strings.Builder
	iter := m.All()
	_, fe, ok := iter.Next()
	for ok {
		b.WriteString(fe.Address.String() + "/" + string(fe.Type))
		_, fe, ok = iter.Next()
		if ok {
			b.WriteString(", ")
		}
	}
	return b.String()
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

// FIXME remove this once statedb bumped and don't need this
// This is broken.
func l3n4AddrFromBytes(bs []byte) loadbalancer.L3n4Addr {
	var l3n4 loadbalancer.L3n4Addr
	a16 := [16]byte(bs[0:16])
	addr := netip.AddrFrom16(a16)
	l3n4.AddrCluster = cmtypes.AddrClusterFrom(addr, 0)
	l3n4.Port = binary.BigEndian.Uint16(bs[16:18])
	l3n4.Protocol = string(bs[18:21])
	return l3n4
}
