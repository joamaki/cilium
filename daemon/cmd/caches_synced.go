// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cmd

import (
	"go.uber.org/fx"

	k8sTypes "github.com/cilium/cilium/pkg/k8s/types"
)

// cachesSyncedModule wraps a channel that is closed when K8s caches have been synced. This decouples
// it from the legacy daemon initialization and helps with lifting modules out from it.
var cachesSyncedModule = fx.Module(
	"caches-synced",
	fx.Provide(newCachesSynced),
)

type CachesSynced chan struct{}

func newCachesSynced() (CachesSynced, k8sTypes.K8sSyncedChecker) {
	ch := CachesSynced(make(chan struct{}))
	return ch, ch
}

func (ch CachesSynced) Chan() chan struct{} {
	return ch
}

func (ch CachesSynced) Close() {
	close(ch)
}

// K8sCacheIsSynced returns true if the agent has fully synced its k8s cache
// with the API server
func (ch CachesSynced) K8sCacheIsSynced() bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (ch CachesSynced) WaitForK8sCacheSync() {
	<-ch
}
