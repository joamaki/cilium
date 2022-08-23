// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"context"
	"net"

	"github.com/cilium/cilium/pkg/cidr"
)

// LocalNodeStore provides access to information about the local node.
type LocalNodeStore interface {
	// Observe observes changes to the local node.
	Observe(ctx context.Context, next func(*LocalNode) error) error

	// Update modifies the local node with a mutator. On modification
	// a copy of the previous version is made and that is then mutated,
	// and finally passed to observers.
	Update(func(*LocalNode))
}

type LocalNode struct {
	IPv4Loopback      net.IP
	IPv4Address       net.IP
	IPv4RouterAddress net.IP
	IPv6Address       net.IP
	IPv6RouterAddress net.IP
	IPv4AllocRange    *cidr.CIDR
	IPv6AllocRange    *cidr.CIDR

	// k8s Node External IP
	IPv4ExternalAddress net.IP
	IPv6ExternalAddress net.IP

	// Addresses of the cilium-health endpoint located on the node
	IPv4HealthIP net.IP
	IPv6HealthIP net.IP

	// Addresses of the Cilium Ingress located on the node
	IPv4IngressIP net.IP
	IPv6IngressIP net.IP

	// k8s Node IP (either InternalIP or ExternalIP or nil; the former is preferred)
	K8sNodeIP net.IP

	IPSecKeyIdentity uint8

	WireguardPubKey string
}
