// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package ipcache

import (
	"net"

	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/identity/cache"
	ipcacheTypes "github.com/cilium/cilium/pkg/ipcache/types"
	k8sTypes "github.com/cilium/cilium/pkg/k8s/types"
	"github.com/cilium/cilium/pkg/source"
)

type IPCacheLooker interface {
	// LookupByIP returns the corresponding security identity that endpoint IP maps
	// to within the provided IPCache, as well as if the corresponding entry exists
	// in the IPCache.
	LookupByIP(IP string) (Identity, bool)

	// LookupByPrefix returns the corresponding security identity that endpoint IP
	// maps to within the provided IPCache, as well as if the corresponding entry
	// exists in the IPCache.
	LookupByPrefix(IP string) (Identity, bool)

	// LookupByIdentity returns the set of IPs (endpoint or CIDR prefix) that have
	// security identity ID, or nil if the entry does not exist.
	LookupByIdentity(id identity.NumericIdentity) (ips []string)
}

type IPCacheModifier interface {
	// Upsert adds / updates the provided IP (endpoint or CIDR prefix) and identity
	// into the IPCache.
	//
	// Returns an error if the entry is not owned by the self declared source, i.e.
	// returns error if the kubernetes layer is trying to upsert an entry now
	// managed by the kvstore layer or if 'ip' is invalid. See
	// source.AllowOverwrite() for rules on ownership. hostIP is the location of the
	// given IP. It is optional (may be nil) and is propagated to the listeners.
	// k8sMeta contains Kubernetes-specific metadata such as pod namespace and pod
	// name belonging to the IP (may be nil).
	Upsert(ip string, hostIP net.IP, hostKey uint8, k8sMeta *K8sMetadata, newIdentity Identity) (namedPortsChanged bool, err error)

	// DeleteOnMetadataMatch removes the provided IP to security identity mapping from the IPCache
	// if the metadata cache holds the same "owner" metadata as the triggering pod event.
	DeleteOnMetadataMatch(IP string, source source.Source, namespace, name string) (namedPortsChanged bool)

	// Delete removes the provided IP-to-security-identity mapping from the IPCache.
	Delete(IP string, source source.Source) (namedPortsChanged bool)
}

var Module = fx.Module(
	"ipcache",
	fx.Provide(
		//newIPCacheForModule,
		newGetIPHandler,
	),
)

// TODO dependencies that currently go through globals:
// - kvstore
// -

// TODO missing functionality/issues:
// - a way to stop ipcache
// - IPIdentityWatcher initialization (global sync.Once!), should probably
//   be a submodule of IPCache with its own OnStart.
// -

type ipcacheParams struct {
	fx.In

	cache.IdentityAllocator
	k8sTypes.K8sSyncedChecker
	ipcacheTypes.PolicyHandler
	ipcacheTypes.DatapathHandler
}

func newIPCacheForModule(p ipcacheParams) (*IPCache, IPCacheLooker, IPCacheModifier) {
	c := &Configuration{
		IdentityAllocator: p.IdentityAllocator,
		PolicyHandler:     p.PolicyHandler,
		DatapathHandler:   p.DatapathHandler,
	}
	ipc := NewIPCache(c)
	ipc.RegisterK8sSyncedChecker(p.K8sSyncedChecker)

	return ipc, ipc, ipc
}
