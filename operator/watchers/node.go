package watchers

import (
	"context"
	"sync"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
)

var (
	// nodeSyncOnce is used to make sure nodesInit is only setup once.
	nodeSyncOnce sync.Once

	// slimNodeStore contains all cluster nodes store as slim_core.Node
	slimNodeStore resource.Store[*slim_corev1.Node]

	// slimNodeStoreSynced is closed once the slimNodeStore is synced
	// with k8s.
	slimNodeStoreSynced = make(chan struct{})

	nodeController cache.Controller
)

type slimNodeGetter interface {
	GetK8sSlimNode(nodeName string) (*slim_corev1.Node, error)
}

type nodeGetter struct{}

// GetK8sSlimNode returns a slim_corev1.Node from the local store.
// The return structure should only be used for read purposes and should never
// be written into it.
func (nodeGetter) GetK8sSlimNode(nodeName string) (*slim_corev1.Node, error) {
	node, exists, err := slimNodeStore.GetByKey(resource.Key{Name: nodeName})
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, k8sErrors.NewNotFound(schema.GroupResource{
			Group:    "core",
			Resource: "Node",
		}, nodeName)
	}
	return node, nil
}

// nodesInit starts up a node watcher to handle node events.
func nodesInit(nodes resource.Resource[*slim_corev1.Node]) {
	var err error

	slimNodeStore, err = nodes.Store(context.TODO())
	if err != nil {
		log.WithError(err).Fatal("Could not retrieve node store")
	}
	close(slimNodeStoreSynced)
}
