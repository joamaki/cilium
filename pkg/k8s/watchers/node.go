// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package watchers

import (
	"context"

	v1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/cilium/cilium/pkg/comparator"
	"github.com/cilium/cilium/pkg/k8s/resource"
	"github.com/cilium/cilium/pkg/k8s/watchers/resources"
	"github.com/cilium/cilium/pkg/k8s/watchers/subscriber"
	"github.com/cilium/cilium/pkg/lock"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
)

// RegisterNodeSubscriber allows registration of subscriber.Node implementations.
// On k8s Node events all registered subscriber.Node implementations will
// have their event handling methods called in order of registration.
func (k *K8sWatcher) RegisterNodeSubscriber(s subscriber.Node) {
	k.NodeChain.Register(s)
}

// The NodeUpdate interface is used to provide an abstraction for the
// nodediscovery.NodeDiscovery object logic used to update a node entry in the
// KVStore and the k8s CiliumNode.
type NodeUpdate interface {
	UpdateLocalNode()
}

func nodeEventsAreEqual(oldNode, newNode *v1.Node) bool {
	if !comparator.MapStringEquals(oldNode.GetLabels(), newNode.GetLabels()) {
		return false
	}

	return true
}

func (k *K8sWatcher) NodesInit() {
	apiGroup := k8sAPIGroupNodeV1Core
	k.nodesInitOnce.Do(func() {

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-k.stop
			cancel()
		}()

		swg := lock.NewStoppableWaitGroup()

		synced := false

		k.blockWaitGroupToSyncResources(
			k.stop,
			swg,
			func() bool { return synced },
			apiGroup,
		)

		var oldNode *v1.Node

		k.sharedResources.LocalNode.Observe(
			ctx,
			func(ev resource.Event[*v1.Node]) {
				ev.Handle(
					func() error {
						synced = true
						return nil
					},
					func(_ resource.Key, newNode *v1.Node) error {
						if oldNode == nil {
							k.K8sEventReceived(apiGroup, metricNode, resources.MetricCreate, true, false)
							errs := k.NodeChain.OnAddNode(newNode, swg)
							k.K8sEventProcessed(metricNode, resources.MetricCreate, errs == nil)
						} else {
							equal := nodeEventsAreEqual(oldNode, newNode)
							k.K8sEventReceived(apiGroup, metricNode, resources.MetricUpdate, true, equal)
							if !equal {
								errs := k.NodeChain.OnUpdateNode(oldNode, newNode, swg)
								k.K8sEventProcessed(metricNode, resources.MetricUpdate, errs == nil)
							}
						}
						oldNode = newNode
						return nil
					},
					nil,
				)

			},
			nil, // completion means we're shutting down
		)
	})
}

// GetK8sNode returns the *local Node* from the local store.
func (k *K8sWatcher) GetK8sNode(ctx context.Context, nodeName string) (*v1.Node, error) {
	pName := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
	// Retrieve the store. Blocks until synced (or ctx cancelled).
	store, err := k.sharedResources.LocalNode.Store(ctx)
	if err != nil {
		return nil, err
	}
	node, exists, err := store.Get(pName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, k8sErrors.NewNotFound(schema.GroupResource{
			Group:    "core",
			Resource: "Node",
		}, nodeName)
	}
	return node.DeepCopy(), nil
}

// ciliumNodeUpdater implements the subscriber.Node interface and is used
// to keep CiliumNode objects in sync with the node ones.
type ciliumNodeUpdater struct {
	kvStoreNodeUpdater NodeUpdate
}

func NewCiliumNodeUpdater(kvStoreNodeUpdater NodeUpdate) *ciliumNodeUpdater {
	return &ciliumNodeUpdater{
		kvStoreNodeUpdater: kvStoreNodeUpdater,
	}
}

func (u *ciliumNodeUpdater) OnAddNode(newNode *v1.Node, swg *lock.StoppableWaitGroup) error {
	// We don't need to run OnAddNode because Cilium will fetch the state from
	// k8s upon initialization and will populate the KVStore [1] node with this
	// information or create a Cilium Node CR [2].
	// [1] https://github.com/cilium/cilium/blob/2bea69a54a00f10bec093347900cc66395269154/daemon/cmd/daemon.go#L1102
	// [2] https://github.com/cilium/cilium/blob/2bea69a54a00f10bec093347900cc66395269154/daemon/cmd/daemon.go#L864-L868
	return nil
}

func (u *ciliumNodeUpdater) OnUpdateNode(oldNode, newNode *v1.Node, swg *lock.StoppableWaitGroup) error {
	u.updateCiliumNode(newNode)

	return nil
}

func (u *ciliumNodeUpdater) OnDeleteNode(*v1.Node, *lock.StoppableWaitGroup) error {
	return nil
}

func (u *ciliumNodeUpdater) updateCiliumNode(node *v1.Node) {
	if node.Name != nodeTypes.GetName() {
		// The cilium node updater should only update the information relevant
		// to itself. It should not update any of the other nodes.
		log.Errorf("BUG: trying to update node %q while we should only update for %q", node.Name, nodeTypes.GetName())
		return
	}

	u.kvStoreNodeUpdater.UpdateLocalNode()
}
