// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build mock

package node

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/cilium/cilium/pkg/bpf"
	"github.com/cilium/cilium/pkg/option"
	controlplane "github.com/cilium/cilium/test/control-plane"
)

var (
	node = controlplane.MustUnmarshal(`
apiVersion: v1
kind: Node
metadata:
  name: "node"
spec:
  podCIDR: "10.0.1.0/24"
status:
  addresses:
  - address: "10.0.0.1"
    type: InternalIP
  - address: node
    type: Hostname
`)

	defaultNamespace = controlplane.MustUnmarshal(`
apiVersion: v1
kind: Namespace
metadata:
  name: default
`)

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
)

func newPod(name string) *corev1.Pod {
	trueVal := true

	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID("pod-" + name + "-uid"),
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
					Started:     &trueVal,
				},
			},
			HostIP: "10.0.0.1",
			Phase:  corev1.PodPending,
		},
	}

}

func TestEndpoint(t *testing.T) {
	cpt := controlplane.NewControlPlaneTest(t, "node", "1.24")
	cpt.UpdateObjects(node, defaultNamespace)

	cpt.StartAgent(func(*option.DaemonConfig) {})
	defer cpt.StopAgent()

	t.Run("AddTwoPodsAndPolicy", func(t *testing.T) {
		subtestAddTwoPodsAndPolicy(t, cpt)
	})
}

func subtestAddTwoPodsAndPolicy(t *testing.T, cpt *controlplane.ControlPlaneTest) {

	testPod1, testPod2 := newPod("pod1"), newPod("pod2")

	for i, pod := range []*corev1.Pod{testPod1, testPod2} {
		cpt.UpdateObjects(pod)
		addr := cpt.CreateEndpointForPod(int64(i), pod)
		pod.Status.PodIP = addr
		pod.Status.PodIPs = []corev1.PodIP{{addr}}
		pod.Status.Phase = corev1.PodRunning
		pod.ObjectMeta.Generation += 1
		cpt.UpdateObjects(pod)
	}

	time.Sleep(time.Second * 11)
	cpt.UpdateObjects(testPolicy)
	time.Sleep(time.Second)
	bpf.MockDumpMaps()

	if err := cpt.DeleteEndpoint("pod1-container-id"); err != nil {
		t.Fatalf("DeleteEndpoint(1): %s\n", err)
	}
	if err := cpt.DeleteEndpoint("pod2-container-id"); err != nil {
		t.Fatalf("DeleteEndpoint(2): %s\n", err)
	}
	cpt.DeleteObjects(testPod1, testPod2, testPolicy)

	time.Sleep(time.Second)
	bpf.MockDumpMaps()

	time.Sleep(15 * time.Second)
	bpf.MockDumpMaps()
}
