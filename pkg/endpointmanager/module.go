// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package endpointmanager

import (
	"context"
	"net"
	"sync"
	"time"

	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/endpoint"
	"github.com/cilium/cilium/pkg/endpoint/regeneration"
	"github.com/cilium/cilium/pkg/identity/cache"
	"github.com/cilium/cilium/pkg/ipcache"
	"github.com/cilium/cilium/pkg/k8s/watchers/subscriber"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/policy"
)

type EndpointsLookup interface {
	Lookup(id string) (*endpoint.Endpoint, error)

	// LookupCiliumID looks up endpoint by endpoint ID
	LookupCiliumID(id uint16) *endpoint.Endpoint

	// LookupContainerID looks up endpoint by Docker ID
	LookupContainerID(id string) *endpoint.Endpoint

	// LookupIPv4 looks up endpoint by IPv4 address
	LookupIPv4(ipv4 string) *endpoint.Endpoint

	// LookupIPv6 looks up endpoint by IPv6 address
	LookupIPv6(ipv6 string) *endpoint.Endpoint

	// LookupIP looks up endpoint by IP address
	LookupIP(ip net.IP) (ep *endpoint.Endpoint)

	// LookupPodName looks up endpoint by namespace + pod name
	LookupPodName(name string) *endpoint.Endpoint

	// GetEndpoints returns a slice of all endpoints present in endpoint manager.
	GetEndpoints() []*endpoint.Endpoint

	// EndpointExists returns whether the endpoint with id exists.
	EndpointExists(id uint16) bool

	// GetHostEndpoint returns the host endpoint.
	GetHostEndpoint() *endpoint.Endpoint

	// HostEndpointExists returns true if the host endpoint exists.
	HostEndpointExists() bool
}

type EndpointsModify interface {
	// AddEndpoint takes the prepared endpoint object and starts managing it.
	AddEndpoint(owner regeneration.Owner, ep *endpoint.Endpoint, reason string) (err error)

	AddHostEndpoint(
		ctx context.Context,
		owner regeneration.Owner,
		policyGetter policyRepoGetter,
		ipcache *ipcache.IPCache,
		proxy endpoint.EndpointProxy,
		allocator cache.IdentityAllocator,
		reason, nodeName string,
	) error

	// RestoreEndpoint exposes the specified endpoint to other subsystems via the
	// manager.
	RestoreEndpoint(ep *endpoint.Endpoint) error

	// UpdateReferences updates maps the contents of mappings to the specified endpoint.
	UpdateReferences(ep *endpoint.Endpoint) error

	// RemoveEndpoint stops the active handling of events by the specified endpoint,
	// and prevents the endpoint from being globally acccessible via other packages.
	RemoveEndpoint(ep *endpoint.Endpoint, conf endpoint.DeleteConfig) []error

	// RemoveAll removes all endpoints from the global maps.
	RemoveAll()
}

type EndpointManager interface {
	subscriber.Node

	EndpointResourceSynchronizer

	// InitMetrics hooks the EndpointManager into the metrics subsystem. This can
	// only be done once, globally, otherwise the metrics library will panic.
	InitMetrics()

	EndpointsLookup
	EndpointsModify

	Subscribe(s Subscriber)
	Unsubscribe(s Subscriber)

	// UpdatePolicyMaps returns a WaitGroup which is signaled upon once all endpoints
	// have had their PolicyMaps updated against the Endpoint's desired policy state.
	//
	// Endpoints will wait on the 'notifyWg' parameter before updating policy maps.
	UpdatePolicyMaps(ctx context.Context, notifyWg *sync.WaitGroup) *sync.WaitGroup

	// RegenerateAllEndpoints calls a setState for each endpoint and
	// regenerates if state transaction is valid. During this process, the endpoint
	// list is locked and cannot be modified.
	// Returns a waiting group that can be used to know when all the endpoints are
	// regenerated.
	RegenerateAllEndpoints(regenMetadata *regeneration.ExternalRegenerationMetadata) *sync.WaitGroup

	// WaitForEndpointsAtPolicyRev waits for all endpoints which existed at the time
	// this function is called to be at a given policy revision.
	// New endpoints appearing while waiting are ignored.
	WaitForEndpointsAtPolicyRev(ctx context.Context, rev uint64) error

	// OverrideEndpointOpts applies the given options to all endpoints.
	OverrideEndpointOpts(om option.OptionMap)

	// InitHostEndpointLabels initializes the host endpoint's labels with the
	// node's known labels.
	InitHostEndpointLabels(ctx context.Context)

	// GetPolicyEndpoints returns a map of all endpoints present in endpoint
	// manager as policy.Endpoint interface set for the map key.
	GetPolicyEndpoints() map[policy.Endpoint]struct{}

	// HasGlobalCT returns true if the endpoints have a global CT, false otherwise.
	HasGlobalCT() bool

	// CallbackForEndpointsAtPolicyRev registers a callback on all endpoints that
	// exist when invoked. It is similar to WaitForEndpointsAtPolicyRevision but
	// each endpoint that reaches the desired revision calls 'done' independently.
	// The provided callback should not block and generally be lightweight.
	CallbackForEndpointsAtPolicyRev(ctx context.Context, rev uint64, done func(time.Time)) error
}

// The endpoint manager manages the collection of all locally running endpoints.
// This module provides the EndpointManager, EndpointsLookup and EndpointsModify
// APIs.
var Module = fx.Module(
	"endpoint-manager",
	fx.Provide(newEndpointManager),
)

// PeriodicEndpointGCParams is an optional dependency to the endpoint manager and
// if supplied, enables the periodic garbage collection of unhealthy endpoints.
type PeriodicEndpointGCParams struct {
	Interval    time.Duration
	CheckHealth EndpointCheckerFunc
}

type endpointManagerParams struct {
	fx.In

	Lifecycle      fx.Lifecycle
	EpSynchronizer EndpointResourceSynchronizer
	GcParams       *PeriodicEndpointGCParams `optional:"true"`
}
