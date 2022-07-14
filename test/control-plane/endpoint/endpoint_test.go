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

	initialPod = &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod",
			Namespace: "default",
			Labels: map[string]string{
				"name": "testpod",
				"app":  "test",
			},
			Generation: 1,
		},
		Spec: corev1.PodSpec{},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "testcontainer",
					ContainerID: "foo",
				},
			},
			HostIP: "1.1.1.1",
		},
	}

	updatedPod = &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testpod",
			Namespace: "default",
			Labels: map[string]string{
				"name": "testpod",
				"app":  "test",
			},
			Generation: 2,
		},
		Spec: corev1.PodSpec{},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:        "testcontainer",
					ContainerID: "foo",
				},
			},
			PodIP:  "UPDATE ME",
			HostIP: "1.1.1.1",
		},
	}

	initialObjects = []k8sRuntime.Object{
		minimalNode,
		&corev1.Namespace{
			TypeMeta:   metav1.TypeMeta{Kind: "Namespace", APIVersion: "v1"},
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
		},
	}
)

func TestEndpointAdd(t *testing.T) {
	cpt := controlplane.NewControlPlaneTest(t, "node", "1.21")
	cpt.UpdateObjects(initialObjects...)
	cpt.StartAgent(func(*option.DaemonConfig) {})
	defer cpt.StopAgent()

	cpt.UpdateObjects(initialPod)
	addr := cpt.CreateEndpointForPod(initialPod)
	updatedPod.Status.PodIP = addr

	time.Sleep(time.Second)

	bpf.MockDumpMaps()

	lxcMap, err := lxcmap.DumpToMap()
	if err != nil {
		t.Fatalf("DumpToMap error: %s", err)
	}

	fmt.Printf("Endpoints in lxcmap:\n%v\n", lxcMap)

}

func TestEndpointAgain(t *testing.T) {
	TestEndpointAdd(t)
}
