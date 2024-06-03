// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package k8s_test

import (
	"context"
	"testing"
	"time"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/k8s"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/utils"
)

var (
	testNodeNameIndex = statedb.Index[*corev1.Node, string]{
		Name: "name",
		FromObject: func(obj *corev1.Node) index.KeySet {
			return index.NewKeySet(index.String(obj.Name))
		},
		FromKey: index.String,
		Unique:  true,
	}
)

func newTestNodeTable(db *statedb.DB) (statedb.RWTable[*corev1.Node], error) {
	tbl, err := statedb.NewTable(
		"test-nodes",
		testNodeNameIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

func TestReflector(t *testing.T) {
	var (
		nodeName = "some-node"
		node     = &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:            nodeName,
				ResourceVersion: "0",
			},
			Status: corev1.NodeStatus{
				Phase: "init",
			},
		}
		fakeClient, cs = k8sClient.NewFakeClientset()

		db        *statedb.DB
		nodeTable statedb.Table[*corev1.Node]
	)

	// Create the initial version of the node. Do this before anything
	// starts watching the resources to avoid a race.
	fakeClient.KubernetesFakeClientset.Tracker().Create(
		corev1.SchemeGroupVersion.WithResource("nodes"),
		node.DeepCopy(), "")

	var testTimeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	hive := hive.New(
		cell.Provide(func() k8sClient.Clientset { return cs }),
		cell.Module("test", "test",
			cell.ProvidePrivate(
				func(client k8sClient.Clientset, tbl statedb.RWTable[*corev1.Node]) k8s.ReflectorConfig[*corev1.Node] {
					return k8s.ReflectorConfig[*corev1.Node]{
						BufferSize:     10,
						BufferWaitTime: time.Millisecond,
						ListerWatcher:  utils.ListerWatcherFromTyped(client.CoreV1().Nodes()),
						Transform:      nil,
						QueryAll:       nil,
						Table:          tbl,
					}
				},
				newTestNodeTable,
			),
			cell.Invoke(
				k8s.RegisterReflector[*corev1.Node],
				func(db_ *statedb.DB, nodeTable_ statedb.RWTable[*corev1.Node]) {
					db = db_
					nodeTable = nodeTable_
				}),
		),
	)

	tlog := hivetest.Logger(t)
	if err := hive.Start(tlog, ctx); err != nil {
		t.Fatalf("hive.Start failed: %s", err)
	}

	// Wait until the table has been initialized.
	require.Eventually(
		t,
		func() bool { return nodeTable.Initialized(db.ReadTxn()) },
		time.Second,
		5*time.Millisecond)

	iter, watch := nodeTable.AllWatch(db.ReadTxn())
	nodes := statedb.Collect(iter)
	require.Len(t, nodes, 1)
	require.Equal(t, nodeName, nodes[0].Name)

	// Update the node and check that it updated.
	node.Status.Phase = "update1"
	node.ObjectMeta.ResourceVersion = "1"
	fakeClient.KubernetesFakeClientset.Tracker().Update(
		corev1.SchemeGroupVersion.WithResource("nodes"),
		node.DeepCopy(), "")

	// Wait until updated.
	<-watch

	iter, watch = nodeTable.AllWatch(db.ReadTxn())
	nodes = statedb.Collect(iter)

	require.Len(t, nodes, 1)
	require.EqualValues(t, "update1", nodes[0].Status.Phase)

	// Finally delete the node
	fakeClient.KubernetesFakeClientset.Tracker().Delete(
		corev1.SchemeGroupVersion.WithResource("nodes"),
		"", "some-node")

	<-watch

	iter, _ = nodeTable.AllWatch(db.ReadTxn())
	nodes = statedb.Collect(iter)
	require.Len(t, nodes, 0)

	// Finally check that the hive stops correctly. Note that we're not doing this in a
	// defer to avoid potentially deadlocking on the Fatal calls.
	if err := hive.Stop(tlog, context.TODO()); err != nil {
		t.Fatalf("hive.Stop failed: %s", err)
	}
}
