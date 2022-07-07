// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"

	"github.com/cilium/cilium/pkg/cidr"
	fakeDatapath "github.com/cilium/cilium/pkg/datapath/fake"
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

	steps = []*controlplane.ControlPlaneTestStep{
		controlplane.
			NewStep("initial pod").
			AddValidationFunc(validateInitial).
			AddObjects(initialPod),
		controlplane.
			NewStep("updated pod").
			AddValidationFunc(validateInitial).
			AddObjects(updatedPod),
	}

	testCase = controlplane.ControlPlaneTestCase{
		NodeName:          "node",
		InitialObjects:    initialObjects,
		Steps:             steps,
		ValidationTimeout: time.Second,
	}
)

func validateInitial(dp *fakeDatapath.FakeDatapath) error {
	fmt.Printf("identity changes: %#v\n", dp.FakeIPCacheListener())

	for i, change := range dp.FakeIPCacheListener().GetIdentityChanges() {
		fmt.Printf("\t%d: [%s] meta=%v cidr=%s newID=%s\n", i, change.ModType, change.Meta, change.CIDR, change.NewID.ID)
	}

	if len(controlplane.HACKEndpoints) > 0 {
		updatedPod.Status.PodIP = controlplane.HACKEndpoints[0]
	}

	time.Sleep(time.Second)
	return nil
}

func TestEndpointAdd(t *testing.T) {
	testCase.Run(t, "1.21", func(*option.DaemonConfig) {})
}
