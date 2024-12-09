package k8s

import (
	"fmt"

	"github.com/cilium/cilium/pkg/loadbalancer"
)

// ServiceID identifies the Kubernetes service
type ServiceID struct {
	Cluster   string `json:"cluster,omitempty"`
	Name      string `json:"serviceName,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// String returns the string representation of a service ID
func (s ServiceID) String() string {
	if s.Cluster != "" {
		return fmt.Sprintf("%s/%s/%s", s.Cluster, s.Namespace, s.Name)
	}
	return fmt.Sprintf("%s/%s", s.Namespace, s.Name)
}

// EndpointSliceID identifies a Kubernetes EndpointSlice as well as the legacy
// v1.Endpoints.
type EndpointSliceID struct {
	ServiceID
	EndpointSliceName string
}

// +deepequal-gen=true
type NodePortToFrontend map[string]*loadbalancer.L3n4AddrID
