// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quota

// HostPorts limits HostPort usage per namespace on each node.
const HostPorts ResourceName = "cilium.io/hostports-per-node"

// ServicePorts limits Kubernetes Service port units per namespace.
const ServicePorts ResourceName = "cilium.io/service-ports"
