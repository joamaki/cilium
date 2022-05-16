// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package egressgateway

import (
	"go.uber.org/fx"

	k8sTypes "github.com/cilium/cilium/pkg/k8s/types"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
)

type EgressGatewayHandlers interface {
	// OnAddEgressPolicy parses the given policy config, and updates internal state
	// with the config fields.
	OnAddEgressPolicy(config PolicyConfig)

	// OnDeleteEgressPolicy deletes the internal state associated with the given
	// policy, including egress eBPF map entries.
	OnDeleteEgressPolicy(configID policyID)

	// OnUpdateEndpoint is the event handler for endpoint additions and updates.
	OnUpdateEndpoint(endpoint *k8sTypes.CiliumEndpoint)

	// OnDeleteEndpoint is the event handler for endpoint deletions.
	OnDeleteEndpoint(endpoint *k8sTypes.CiliumEndpoint)

	// OnUpdateNode is the event handler for node additions and updates.
	OnUpdateNode(node nodeTypes.Node)

	// OnDeleteNode is the event handler for node deletions.
	OnDeleteNode(node nodeTypes.Node)
}

var Module = fx.Module(
	"egress-gateway",

	fx.Provide(
		fx.Annotate(
			newEgressGatewayManager,
			fx.As(new(EgressGatewayHandlers)),
		),
	),
)
