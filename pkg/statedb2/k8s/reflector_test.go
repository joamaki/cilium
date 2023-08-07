// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package k8s_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/statedb2"
	"github.com/cilium/cilium/pkg/statedb2/k8s"
)

func TestK8sTableCell(t *testing.T) {
	type params struct {
		cell.In

		DB        *statedb2.DB
		Table     statedb2.Table[*v1.Pod]
		Index     statedb2.Index[*v1.Pod, k8s.Key]
		Clientset client.Clientset
	}
	var p params

	h := hive.New(
		client.FakeClientCell,
		statedb2.Cell,
		k8s.NewK8sTableCell[*v1.Pod](
			"pods",
			func(cs client.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*v1.PodList](
					cs.CoreV1().Pods(""),
				)
			},
		),
		cell.Invoke(func(p_ params) { p = p_ }),
	)

	require.NoError(t, h.Start(context.TODO()))

	table := p.Table
	index := p.Index

	// Table is empty when starting.
	txn := p.DB.ReadTxn()
	iter, watch := table.All(txn)
	objs := statedb2.Collect[*v1.Pod](iter)
	assert.Len(t, objs, 0)

	// Insert a new pod and wait for table to update.
	expectedPod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "pod1", Namespace: "test1",
			Labels: map[string]string{
				"foo": "bar",
			},
		},
	}
	_, err := p.Clientset.CoreV1().Pods("test1").Create(
		context.Background(), expectedPod, metav1.CreateOptions{})
	if !assert.NoError(t, err) {
		t.FailNow()
	}

	<-watch

	// Table should now contain the new pod
	txn = p.DB.ReadTxn()
	iter, watch = table.All(txn)
	objs = statedb2.Collect[*v1.Pod](iter)
	assert.Len(t, objs, 1)

	// Pod can be retrieved by name
	pod, _, ok := table.First(txn, index.Query(k8s.Key{Namespace: expectedPod.Namespace, Name: expectedPod.Name}))
	if assert.True(t, ok) && assert.NotNil(t, pod) {
		assert.Equal(t, expectedPod.Name, pod.Name)
	}

	// Pod deletion can be observed
	iter, watch = table.All(txn)
	err = p.Clientset.CoreV1().Pods("test1").Delete(context.Background(), "pod1", metav1.DeleteOptions{})
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	<-watch

	iter, _ = table.All(p.DB.ReadTxn())
	objs = statedb2.Collect[*v1.Pod](iter)
	assert.Len(t, objs, 0)

	assert.NoError(t, h.Stop(context.TODO()))
}
