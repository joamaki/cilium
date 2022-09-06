// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package resources

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/stream"
)

var node = &corev1.Node{
	ObjectMeta: metav1.ObjectMeta{
		Name:            "hello",
		ResourceVersion: "1",
	},
	Status: corev1.NodeStatus{
		Phase: "funky",
	},
}

func testStore(t *testing.T, store Store[*corev1.Node]) {
	var (
		item   *corev1.Node
		exists bool
		err    error
	)

	check := func() {
		if err != nil {
			t.Fatalf("unexpected error from GetByKey: %s", err)
		}
		if !exists {
			t.Fatalf("GetByKey returned exists=false")
		}
		if item.Name != node.ObjectMeta.Name {
			t.Fatalf("expected item returned by GetByKey to have name %s, got %s",
				node.ObjectMeta.Name, item.ObjectMeta.Name)
		}
	}
	item, exists, err = store.GetByKey(node.ObjectMeta.Name)
	check()
	item, exists, err = store.Get(node)
	check()

	keys := store.ListKeys()
	if len(keys) != 1 && keys[0] != "hello" {
		t.Fatalf("unexpected keys: %#v", keys)
	}

	items := store.List()
	if len(items) != 1 && items[0].ObjectMeta.Name != "hello" {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func TestK8sResourceWithFakeClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cs := fake.NewSimpleClientset(node)

	nodesLW := utils.ListerWatcherFromTyped[*corev1.NodeList](cs.CoreV1().Nodes())
	events, run := NewResource[*corev1.Node](ctx, nodesLW)
	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Wait()
	go func() { run(); wg.Done() }()

	errs := make(chan error)
	xs := stream.ToChannel(ctx, errs, events)

	// First event should be the node (initial set)
	(<-xs).Dispatch(
		func(_ Store[*corev1.Node]) { t.Fatal("unexpected sync") },
		func(key Key, node *corev1.Node) {
			if key.String() != "hello" {
				t.Fatalf("unexpected update of %s", key)
			}
			if node.GetName() != "hello" {
				t.Fatalf("unexpected node name: %#v", node)
			}
			if node.Status.Phase != "funky" {
				t.Fatalf("unexpected status in node: %s", node.Status.Phase)
			}
		},
		func(key Key) { t.Fatalf("unexpected delete of %s", key) },
	)

	// Second should be a sync.
	(<-xs).Dispatch(
		func(s Store[*corev1.Node]) {
		},
		func(key Key, node *corev1.Node) {
			t.Fatalf("unexpected update of %s", key)
		},
		func(key Key) {
			t.Fatalf("unexpected delete of %s", key)
		},
	)

	// Update the node and verify the event
	node.Status.Phase = "groovy"
	node.ObjectMeta.ResourceVersion = "2"

	cs.Tracker().Update(
		corev1.SchemeGroupVersion.WithResource("nodes"),
		node, "")
	(<-xs).Dispatch(
		func(_ Store[*corev1.Node]) { t.Fatalf("unexpected sync") },
		func(key Key, node *corev1.Node) {
			if key.String() != "hello" {
				t.Fatalf("unexpected update of %s", key)
			}
			if node.Status.Phase != "groovy" {
				t.Fatalf("unexpected status in node: %s", node.Status.Phase)
			}
		},
		func(key Key) {
			if key.String() != "hello" {
				t.Fatalf("unexpected key in delete of node: %s", key)
			}
		},
	)

	// Finally delete the node and verify the event
	cs.Tracker().Delete(
		corev1.SchemeGroupVersion.WithResource("nodes"),
		"", "hello")
	(<-xs).Dispatch(
		func(_ Store[*corev1.Node]) { t.Fatalf("unexpected sync") },
		func(key Key, node *corev1.Node) {
			t.Fatalf("unexpected update of %s", key)
		},
		func(key Key) {
			if key.String() != "hello" {
				t.Fatalf("unexpected key in delete of node: %s", key)
			}
		},
	)

	cancel()

	err := <-errs
	if err != nil {
		t.Fatalf("expected nil error, got %s", err)
	}

}
