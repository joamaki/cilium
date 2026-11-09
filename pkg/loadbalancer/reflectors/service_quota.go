// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package reflectors

import (
	"net/netip"

	slim "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/quota"
)

func serviceReservationID(name loadbalancer.ServiceName) quota.ReservationID {
	return quota.ReservationID("service/" + name.String())
}

// countServicePorts is independent of node configuration: each advertised
// address/port/protocol pair counts once, and each NodePort counts once across
// address families. Backend changes and internal traffic-policy scopes do not
// affect the charge. LoadBalancer addresses count once advertised as VIPs.
// HealthCheckNodePort belongs to the health server, not the LB service map.
func countServicePorts(svc *slim.Service) uint64 {
	if isHeadless(svc) {
		return 0
	}
	addresses := map[netip.Addr]struct{}{}
	add := func(s string) {
		if ip, err := netip.ParseAddr(s); err == nil {
			addresses[ip.Unmap()] = struct{}{}
		}
	}
	if len(svc.Spec.ClusterIPs) > 0 {
		for _, s := range svc.Spec.ClusterIPs {
			add(s)
		}
	} else {
		add(svc.Spec.ClusterIP)
	}
	for _, s := range svc.Spec.ExternalIPs {
		add(s)
	}
	if svc.Spec.Type == slim.ServiceTypeLoadBalancer {
		for _, ingress := range svc.Status.LoadBalancer.Ingress {
			if ingress.IPMode == nil || *ingress.IPMode == slim.LoadBalancerIPModeVIP {
				add(ingress.IP)
			}
		}
	}
	type port struct {
		number   int32
		protocol slim.Protocol
	}
	ports := map[port]struct{}{}
	nodePorts := map[port]struct{}{}
	for _, p := range svc.Spec.Ports {
		proto := p.Protocol
		if proto == "" {
			proto = slim.ProtocolTCP
		}
		ports[port{p.Port, proto}] = struct{}{}
		if p.NodePort > 0 {
			nodePorts[port{p.NodePort, proto}] = struct{}{}
		}
	}
	return uint64(len(addresses))*uint64(len(ports)) + uint64(len(nodePorts))
}
