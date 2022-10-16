// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package endpointmanager

import (
	"context"

	"github.com/cilium/cilium/pkg/endpoint"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s/watchers"
	"github.com/cilium/cilium/pkg/option"
)

var Cell = cell.Module(
	"Endpoint Manager",

	cell.Provide(newDefaultEndpointManager),
)

func newDefaultEndpointManager(lc hive.Lifecycle) *EndpointManager {
	ctx, cancel := context.WithCancel(context.Background())

	checker := endpoint.CheckHealth

	mgr := NewEndpointManager(&watchers.EndpointSynchronizer{})
	if option.Config.EndpointGCInterval > 0 {
		mgr = mgr.WithPeriodicEndpointGC(ctx, checker, option.Config.EndpointGCInterval)
	}

	mgr.InitMetrics()

	lc.Append(hive.Hook{
		OnStop: func(context.Context) error {
			cancel()
			return nil
		},
	})

	return mgr
}
