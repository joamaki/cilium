// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cmd

import (
	"github.com/cilium/cilium/pkg/backoff"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/endpointmanager"
	"github.com/cilium/cilium/pkg/gops"
	"github.com/cilium/cilium/pkg/hive/cell"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/kvstore"
	"github.com/cilium/cilium/pkg/node"
	nodeManager "github.com/cilium/cilium/pkg/node/manager"
)

var (
	Agent = cell.Module(
		"Cilium Agent",

		Infrastructure,
		ControlPlane,
		Datapath,
	)

	// Infrastructure provides access and services to the outside.
	// A cell should live here instead of ControlPlane if it is not needed by
	// integrations tests, or needs to be mocked.
	Infrastructure = cell.Module(
		"Infrastructure",

		// Runs the gops agent, a tool to diagnose Go processes.
		gops.Cell(defaults.GopsPortAgent),

		// Provides Clientset, API for accessing Kubernetes objects.
		k8sClient.Cell,

		// Provides cluster-size dependent backoff. The node count is managed.
		// by NodeManager.
		backoff.Cell,

		// Provides BackendOperations for accessing the key-value store
		cell.ProvidePrivate(kvstoreExtraOptions),
		kvstore.Cell,
	)

	// ControlPlane implement the per-node control functions. These are pure
	// business logic and depend on datapath or infrastructure to perform
	// actions. This separation enables non-privileged integration testing of
	// the control-plane.
	ControlPlane = cell.Module(
		"Control Plane",

		// LocalNodeStore holds onto the information about the local node and allows
		// observing changes to it.
		node.LocalNodeStoreCell,

		// EndpointManager maintains a collection of the locally running endpoints.
		endpointmanager.Cell,

		// NodeManager maintains a collection of other nodes in the cluster.
		nodeManager.Cell,

		// daemonCell wraps the legacy daemon initialization and provides Promise[*Daemon].
		daemonCell,

		// netconfCell provides NetConf, the Cilium specific CNI network configuration.
		// This cell should be kept as last to make sure the CNI configuration is written
		// out after everything has started.
		netconfCell,
	)

	// Datapath provides the privileged operations to apply control-plane
	// decision to the kernel.
	Datapath = cell.Module(
		"Datapath",

		cell.Provide(newDatapath),
	)
)

func kvstoreExtraOptions(csb *backoff.ClusterSizeBackoff, cfg kvstore.KVStoreConfig) *kvstore.ExtraOptions {
	// FIXME: Remove the ExtraOptions and make kvstore depend on ClusterSizeBackoff directly.
	opts := &kvstore.ExtraOptions{
		ClusterSizeDependantInterval: csb.ClusterSizeDependantInterval,
	}

	// TODO: k8s service dialer. Need to split up the connecting to start hook and
	// implement a custom dialer that waits for service cache to be ready.
	// see initKVStore().
	//
	//
	return opts
}
