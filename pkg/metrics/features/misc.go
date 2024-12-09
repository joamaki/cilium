// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package features

import (
	v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
)

func (m Metrics) AddCNP(_ *v2.CiliumNetworkPolicy) {
	m.NPCNPIngested.WithLabelValues(actionAdd).Inc()
}

func (m Metrics) DelCNP(_ *v2.CiliumNetworkPolicy) {
	m.NPCNPIngested.WithLabelValues(actionDel).Inc()
}

func (m Metrics) AddCCNP(_ *v2.CiliumNetworkPolicy) {
	m.NPCCNPIngested.WithLabelValues(actionAdd).Inc()
}

func (m Metrics) DelCCNP(_ *v2.CiliumNetworkPolicy) {
	m.NPCCNPIngested.WithLabelValues(actionDel).Inc()
}

func (m Metrics) AddClusterMeshConfig(clusterMeshMode string, maxConnectedClusters string) {
	m.ACLBClusterMeshEnabled.WithLabelValues(clusterMeshMode, maxConnectedClusters).Inc()
}

func (m Metrics) DelClusterMeshConfig(clusterMeshMode string, maxConnectedClusters string) {
	m.ACLBClusterMeshEnabled.WithLabelValues(clusterMeshMode, maxConnectedClusters).Dec()
}
