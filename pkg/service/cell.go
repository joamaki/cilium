// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package service

import (
	"github.com/cilium/hive/cell"

	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	monitorAgent "github.com/cilium/cilium/pkg/monitor/agent"
	"github.com/cilium/cilium/pkg/testutils/mockmaps"
)

// Cell provides access to the Service Manager.
var Cell = cell.Module(
	"service-manager",
	"Service Manager",

	cell.ProvidePrivate(newServiceInternal),
	cell.Provide(func(svc *Service) ServiceManager { return svc }),
	cell.Provide(func(svc *Service) ServiceHealthCheckManager { return svc }),

	cell.ProvidePrivate(func(sm ServiceManager) syncNodePort { return sm }),
	cell.Invoke(registerServiceReconciler),
)

type serviceManagerParams struct {
	cell.In

	ExpConfig    experimental.Config
	Datapath     types.Datapath
	MonitorAgent monitorAgent.Agent

	HealthCheckers []HealthChecker `group:"healthCheckers"`
}

func newServiceInternal(params serviceManagerParams) *Service {
	enabledHealthCheckers := []HealthChecker{}
	for _, hc := range params.HealthCheckers {
		if hc != nil {
			enabledHealthCheckers = append(enabledHealthCheckers, hc)
		}
	}
	lbmap := params.Datapath.LBMap()
	if params.ExpConfig.EnableExperimentalLB {
		// The experimental control-plane is enabled. Use a fake LBMap
		// to effectively disable this code path.
		lbmap = mockmaps.NewLBMockMap()
	}
	return newService(params.MonitorAgent, lbmap, params.Datapath.NodeNeighbors(), enabledHealthCheckers)
}
