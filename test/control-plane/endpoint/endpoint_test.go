// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build mock

package node

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"

	"github.com/cilium/cilium/pkg/bpf"
	"github.com/cilium/cilium/pkg/cidr"
	"github.com/cilium/cilium/pkg/maps/lxcmap"
	"github.com/cilium/cilium/pkg/option"
	controlplane "github.com/cilium/cilium/test/control-plane"
)

var (
	t = true

	podCIDR = cidr.MustParseCIDR("1.1.1.0/24")

	minimalNode = &corev1.Node{
		TypeMeta:   metav1.TypeMeta{Kind: "Node", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "node"},
		Spec: corev1.NodeSpec{
			PodCIDR:  podCIDR.String(),
			PodCIDRs: []string{podCIDR.String()},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{},
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "1.1.1.1"},
				{Type: corev1.NodeHostName, Address: "node"},
			},
		},
	}

	testPolicy = controlplane.MustUnmarshal(`
apiVersion: "cilium.io/v2"
kind: CiliumNetworkPolicy
metadata:
  name: "isolate-ns-default"
  namespace: "default"
spec:
  endpointSelector:
    matchLabels: {}
  ingress:
  - fromEndpoints:
    - matchLabels: {}
  - toEndpoints:
    - matchLabels: {}
`)

	initialObjects = []k8sRuntime.Object{
		minimalNode,
		&corev1.Namespace{
			TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
		},
	}
)

func newPod(name string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				"name": name,
				"app":  "test",
			},
			Generation: 1,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: name + "-container",
					Ports: []corev1.ContainerPort{
						{Name: "http", ContainerPort: 80, Protocol: "TCP"},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        name + "-container",
					ContainerID: name + "-container-id",
					Ready:       true,
					Started:     &t,
				},
			},
			HostIP: "1.1.1.1",
			Phase:  corev1.PodPending,
		},
	}

}

func TestEndpointAdd(t *testing.T) {
	cpt := controlplane.NewControlPlaneTest(t, "node", "1.24")
	cpt.UpdateObjects(initialObjects...)
	cpt.StartAgent(func(*option.DaemonConfig) {})
	defer cpt.StopAgent()

	testPod1, testPod2 := newPod("pod1"), newPod("pod2")

	for _, pod := range []*corev1.Pod{testPod1, testPod2} {
		cpt.UpdateObjects(pod)
		addr := cpt.CreateEndpointForPod(pod)
		pod.Status.PodIP = addr
		pod.Status.Phase = corev1.PodRunning
		pod.ObjectMeta.Generation += 1
		cpt.UpdateObjects(pod)
	}

	time.Sleep(time.Second)
	cpt.UpdateObjects(testPolicy)

	for i := 0; i < 10; i++ {
		time.Sleep(time.Second * 5)
		bpf.MockDumpMaps()
	}

	lxcMap, err := lxcmap.DumpToMap()
	if err != nil {
		t.Fatalf("DumpToMap error: %s", err)
	}

	fmt.Printf("Endpoints in lxcmap:\n%v\n", lxcMap)

}
