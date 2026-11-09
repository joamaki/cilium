// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package reflectors

import (
	slim "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCountServicePorts(t *testing.T) {
	proxy := slim.LoadBalancerIPModeProxy
	for _, tc := range []struct {
		name string
		svc  slim.Service
		want uint64
	}{
		{name: "external name", svc: slim.Service{Spec: slim.ServiceSpec{Type: slim.ServiceTypeExternalName}}},
		{name: "headless", svc: slim.Service{Spec: slim.ServiceSpec{ClusterIP: "None", Ports: []slim.ServicePort{{Port: 80, NodePort: 30080}}}}},
		{name: "single family", svc: slim.Service{Spec: slim.ServiceSpec{ClusterIP: "10.0.0.1", Ports: []slim.ServicePort{{Port: 80}}}}, want: 1},
		{name: "dual stack NodePort", svc: slim.Service{Spec: slim.ServiceSpec{Type: slim.ServiceTypeNodePort, ClusterIPs: []string{"10.0.0.1", "fd00::1"}, Ports: []slim.ServicePort{{Port: 80, NodePort: 30080}}}}, want: 3},
		{name: "deduplicate", svc: slim.Service{Spec: slim.ServiceSpec{ClusterIPs: []string{"10.0.0.1", "10.0.0.1"}, ExternalIPs: []string{"10.0.0.1"}, Ports: []slim.ServicePort{{Port: 80}, {Port: 80, Protocol: slim.ProtocolTCP}, {Port: 80, Protocol: slim.ProtocolUDP}}}}, want: 2},
		{name: "load balancer", svc: slim.Service{Spec: slim.ServiceSpec{Type: slim.ServiceTypeLoadBalancer, ClusterIP: "10.0.0.1", ExternalIPs: []string{"192.0.2.1"}, Ports: []slim.ServicePort{{Port: 80, NodePort: 30080}}}, Status: slim.ServiceStatus{LoadBalancer: slim.LoadBalancerStatus{Ingress: []slim.LoadBalancerIngress{{IP: "192.0.2.1"}, {IP: "192.0.2.2"}, {Hostname: "example.com"}, {IP: "192.0.2.3", IPMode: &proxy}}}}}, want: 4},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, countServicePorts(&tc.svc)) })
	}
}
