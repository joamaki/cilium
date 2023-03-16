package services

import (
	"github.com/cilium/cilium/memdb/state/structs"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
)

// TODO: Or better just to redefine? Services may also be coming from
// kvstore or via REST API, so it's perhaps silly to single out K8s as
// the source of truth here.
type ServiceType = slim_corev1.ServiceType

var (
	ServiceTypeClusterIP    = slim_corev1.ServiceTypeClusterIP
	ServiceTypeNodePort     = slim_corev1.ServiceTypeNodePort
	ServiceTypeLoadBalancer = slim_corev1.ServiceTypeLoadBalancer
	ServiceTypeExternalName = slim_corev1.ServiceTypeExternalName
)

type Service struct {
	structs.ExtMeta

	IPs   []structs.IPAddr
	Ports []uint16
	Type  ServiceType
}
