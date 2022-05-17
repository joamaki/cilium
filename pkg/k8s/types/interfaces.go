// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package types

// k8sCacheIsSynced is an interface for checking if the K8s watcher cache has
// been fully synced.
type K8sSyncedChecker interface {
	K8sCacheIsSynced() bool
	WaitForK8sCacheSync()
}
