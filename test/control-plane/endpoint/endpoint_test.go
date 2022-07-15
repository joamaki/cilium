// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package endpoint

import (
	"fmt"
	"net"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/cilium/cilium/pkg/bpf"
	"github.com/cilium/cilium/pkg/datapath/fake"
	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/maps/ipcache"
	"github.com/cilium/cilium/pkg/maps/lxcmap"
	"github.com/cilium/cilium/pkg/maps/policymap"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/policy/trafficdirection"
	"github.com/cilium/cilium/pkg/u8proto"
	controlplane "github.com/cilium/cilium/test/control-plane"
)

var (
	node = controlplane.MustUnmarshal(`
apiVersion: v1
kind: Node
metadata:
  name: "node"
spec:
  podCIDR: "1.1.1.0/24"
status:
  addresses:
  - address: "10.0.0.2"
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
			Namespace: metav1.NamespaceDefault,
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
			HostIP: fake.IPv4InternalAddress.String(),
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

func eventually(t *testing.T, check func() error) {
	var err error
	for retry := 0; retry < 5; retry++ {
		err = check()
		if err == nil {
			break
		}
		time.Sleep(time.Duration(retry) * time.Second)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func subtestAddTwoPodsAndPolicy(t *testing.T, cpt *controlplane.ControlPlaneTest) {
	testPod1, testPod2 := newPod("pod1"), newPod("pod2")
	var podIPs [2]net.IP
	var policyMaps [2]*policymap.PolicyMap
	var podIdentities [2]uint32

	for i, pod := range []*corev1.Pod{testPod1, testPod2} {
		cpt.UpdateObjects(pod)
		addr := cpt.CreateEndpointForPod(int64(i), pod)
		pod.Status.PodIP = addr
		pod.Status.PodIPs = []corev1.PodIP{{addr}}
		pod.Status.Phase = corev1.PodRunning
		pod.ObjectMeta.Generation += 1
		cpt.UpdateObjects(pod)

		// Wait for ipcache and lxc to be updated
		eventually(t, func() error {
			podIPs[i] = net.ParseIP(addr)

			ipcacheKey := ipcache.NewKey(podIPs[i], net.IPv4Mask(0xff, 0xff, 0xff, 0xff))
			ipcacheRawValue, err := ipcache.IPCache.Lookup(&ipcacheKey)
			if err != nil {
				return err
			}

			podIdentities[i] = ipcacheRawValue.(*ipcache.RemoteEndpointInfo).SecurityIdentity

			endpointKey := lxcmap.NewEndpointKey(podIPs[i])
			lxcValueRaw, err := lxcmap.LXCMap.Lookup(endpointKey)
			if err != nil {
				return err
			}

			lxcValue := lxcValueRaw.(*lxcmap.EndpointInfo)

			policyMapPath := bpf.LocalMapPath(policymap.MapName, lxcValue.LxcID)
			policyMaps[i], err = policymap.Open(policyMapPath)
			if err != nil {
				return err
			}

			// Validate that default ingress policies exist
			if !policyMaps[i].Exists(uint32(identity.ReservedIdentityHost), 0, u8proto.U8proto(0), trafficdirection.Ingress) {
				return fmt.Errorf("ingress policy for host missing")
			}
			if !policyMaps[i].Exists(uint32(identity.ReservedIdentityRemoteNode), 0, u8proto.U8proto(0), trafficdirection.Ingress) {
				return fmt.Errorf("ingress policy for remote node missing")
			}

			return nil
		})

	}

	// Apply a policy to allow the pods to communicate
	cpt.UpdateObjects(testPolicy)

	// Wait for the policy to be applied to the per-endpoint policy maps
	eventually(t, func() error {
		if !policyMaps[0].Exists(uint32(podIdentities[1]), 0, u8proto.U8proto(0), trafficdirection.Ingress) {
			return fmt.Errorf("ingress policy missing for pod2 (id=%d) in pod1 policies", podIdentities[1])
		}
		if !policyMaps[1].Exists(uint32(podIdentities[0]), 0, u8proto.U8proto(0), trafficdirection.Ingress) {
			return fmt.Errorf("ingress policy missing for pod1 (id=%d) in pod2 policies", podIdentities[0])
		}
		return nil
	})

	// XXX: if you want to test these, uncomment and edit map2c.go to correct
	// the path to bpf_lxc.o.
	// Map names from btf with "bpftool btf dump file bpf_lxc.o"
	/*map2c("test_cilium_ipcache", &ipcache.IPCache.Map, "/tmp/map_ipcache.h")
	map2c("test_cilium_lxc", lxcmap.LXCMap, "/tmp/map_lxc.h")*/

	// Delete everything and verify cleanup.
	if err := cpt.DeleteEndpoint("pod1-container-id"); err != nil {
		t.Fatalf("DeleteEndpoint(pod1): %s\n", err)
	}
	if err := cpt.DeleteEndpoint("pod2-container-id"); err != nil {
		t.Fatalf("DeleteEndpoint(pod2): %s\n", err)
	}

	cpt.DeleteObjects(testPod1, testPod2, testPolicy)

	eventually(t, func() error {
		for i := 0; i < len(podIPs); i++ {
			endpointKey := lxcmap.NewEndpointKey(podIPs[i])
			_, err := lxcmap.LXCMap.Lookup(endpointKey)
			if err == nil {
				return fmt.Errorf("pod%d still has lxc entry", i+1)
			}

			ipcacheKey := ipcache.NewKey(podIPs[i], net.IPv4Mask(0xff, 0xff, 0xff, 0xff))
			_, err = ipcache.IPCache.Lookup(&ipcacheKey)
			if err == nil {
				return fmt.Errorf("pod%d still has ipcache entry", i+1)
			}
		}
		return nil
	})
}
