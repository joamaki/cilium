package structs

import (
	"net/netip"
	"time"

	"github.com/google/uuid"
	"golang.org/x/exp/slices"

	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/policy/api"
)

type UUID = string

func NewUUID() UUID {
	return UUID(uuid.New().String())
}

// LabelKey as returned by (labels.Labels).SortedList(). Used
// to index identities.
type LabelKey = string

type ExtMeta struct {
	ID        UUID
	Name      string
	Namespace string

	Revision uint64
	Labels   map[string]string
}

type ExtMetaGetter interface {
	GetName() string
	GetNamespace() string
	GetLabels() map[string]string
}

func (m *ExtMeta) GetName() string              { return m.Name }
func (m *ExtMeta) GetNamespace() string         { return m.Namespace }
func (m *ExtMeta) GetLabels() map[string]string { return m.Labels }

type IPAddr struct {
	netip.Addr
}

func (ip IPAddr) DeepCopy() IPAddr {
	return ip
}

func (ip IPAddr) DeepCopyInto(a *IPAddr) {
	*a = ip
}

type IPGetter interface {
	GetIP() IPAddr
}

type IPToIdentity struct {
	ID       uint32
	IP       IPAddr
	LabelKey LabelKey
	Key      uint8
	Source   string
}

func (i *IPToIdentity) GetIP() IPAddr { return i.IP }

type Node struct {
	ExtMeta

	// TODO: Build indexing for sub-structs and then add NodeSpec?
	Identity uint64
	Address  IPAddr
	Status   string
}

// "policy.cachedSelectorPolicy"
// The link between "NumericIdentity", L4Policy and label set.
type SelectorPolicy struct {
	ID              UUID
	NumericIdentity uint32
	LabelKey        LabelKey
	Revision        uint64

	L4Policy L4Policy
	Labels   labels.LabelArray
}

type L4Policy struct {
	SourceRules []UUID
	// TODO fields here for the "processed" set of rules.
}

func (p *L4Policy) AddSourceRule(id UUID) {
	for _, id2 := range p.SourceRules {
		if id == id2 {
			return
		}
	}
	p.SourceRules = append(p.SourceRules, id)
}

func (p *L4Policy) RemoveSourceRule(id UUID) {
	for i, id2 := range p.SourceRules {
		if id == id2 {
			p.SourceRules = slices.Delete(p.SourceRules, i, i+1)
		}
	}
}

// Endpoint is the control-plane description of an endpoint, consisting of information from the
// orchestrator and the compiled policies.
// +k8s:deepcopy-gen=false
type Endpoint struct {
	ID        UUID
	Namespace string
	Name      string
	Revision  uint64

	CreatedAt   time.Time
	ContainerID string
	IPv4, IPv6  netip.Addr

	// TODO: Should the "immutable" Endpoint data (above fields) be its separate thing and mutating state
	// is separate?

	LabelKey LabelKey
	Labels   labels.OpLabels

	State            EndpointState
	SelectorPolicyID UUID
}

func (e *Endpoint) DeepCopy() *Endpoint {

	return &Endpoint{
		ID:               e.ID,
		Namespace:        e.Namespace,
		Name:             e.Name,
		Revision:         e.Revision,
		CreatedAt:        e.CreatedAt,
		ContainerID:      e.ContainerID,
		IPv4:             e.IPv4,
		IPv6:             e.IPv6,
		LabelKey:         e.LabelKey,
		Labels:           e.Labels,
		State:            e.State,
		SelectorPolicyID: e.SelectorPolicyID,
	}

}

type EndpointState string

var (
	// The endpoint has been just created, but have not yet been prepared.
	EPInit = EndpointState("ep-init")

	// The endpoint is being processed.
	EPProcessing = EndpointState("ep-processing")

	// The endpoint is ready and the datapath can now reconcile it.
	// Idea here being that control-plane deals with getting the endpoint
	// into a consistent state after which the datapath can reconcile it.
	// Need some index for endpoints that are ready but not processed by datapath.
	// (In DatapathEndpoint table?)
	EPReady = EndpointState("ep-ready")
)

// DatapathEndpoint is the internal state of an endpoint for datapath
// reconciliation.
type DatapathEndpoint struct {
	ID UUID

	// TODO how would policies be presented on this side?

	EndpointID UUID
	State      DatapathEndpointState
	IfName     string
	IfIndex    int
}

type DatapathEndpointState string

var (
	// The endpoint has been created, but not initialized yet.
	DESInit = DatapathEndpointState("des-init")

	// The programs for the endpoint are being prepared.
	DESCompiling = DatapathEndpointState("des-compiling")

	// The endpoint is ready to use.
	DESReady = DatapathEndpointState("des-ready")

	// A failure has occurred when initializing the endpoint.
	DESError = DatapathEndpointState("des-error")
)

//
// External structs
//

type ExtNetworkPolicy struct {
	ExtMeta

	// Status of the network policy. This field can be updated
	// by the node to reflect its status back to the orchestration
	// system.
	Status ExtNetworkPolicyStatus
}

type ExtNetworkPolicyStatus struct {
	Nodes map[string]ExtNetworkPolicyNodeStatus
}

type ExtNetworkPolicyNodeStatus struct {
	OK       bool
	Error    string
	Revision uint64
}

type ExtPolicyRule struct {
	ID UUID
	ExtMeta
	*api.Rule

	// Gone marks that the rule has been removed in the external data source.
	// The object will be deleted once this has been processed.
	// (TODO this is making the assumption that there's a single consumer)
	Gone bool
}
