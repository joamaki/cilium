// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import "github.com/cilium/cilium/pkg/loadbalancer"

const (
	// FirstFreeServiceID is the first ID for which the services should be assigned.
	FirstFreeServiceID = loadbalancer.ID(1)

	// MaxSetOfServiceID is maximum number of set of service IDs that can be stored
	// in the kvstore or the local ID allocator.
	MaxSetOfServiceID = loadbalancer.ID(0xFFFF)

	// FirstFreeBackendID is the first ID for which the backend should be assigned.
	// BPF datapath assumes that backend_id cannot be 0.
	FirstFreeBackendID = loadbalancer.ID(1)

	// MaxSetOfBackendID is maximum number of set of backendIDs IDs that can be
	// stored in the local ID allocator.
	MaxSetOfBackendID = loadbalancer.ID(0xFFFFFFFF)
)
