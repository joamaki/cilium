// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package watchers

import (
	"context"
	"sync"

	"go.uber.org/fx"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/cilium/cilium/pkg/endpointmanager"
	"github.com/cilium/cilium/pkg/hive"
	ciliumio "github.com/cilium/cilium/pkg/k8s/apis/cilium.io"
	"github.com/cilium/cilium/pkg/k8s/informer"
	k8s_resources "github.com/cilium/cilium/pkg/k8s/resources"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_metav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	slimclientset "github.com/cilium/cilium/pkg/k8s/slim/k8s/client/clientset/versioned"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/labelsfilter"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/policy"
	"github.com/cilium/cilium/pkg/promise"
)

var NamespaceWatcherCell = hive.NewCell(
	"k8s-watcher-namespace",

	fx.Provide(newNamespaceWatcher),
	fx.Invoke(func(*namespaceWatcher) {}),
)

type namespaceWatcher struct {
	oldLabels map[string]labels.Labels
	epMngr    *endpointmanager.EndpointManager
}

type namespaceLabels struct {
	namespace string
	labels    labels.Labels
}

func newNamespaceWatcher(
	lc fx.Lifecycle,
	nsResource k8s_resources.Resource[*slim_corev1.Namespace],
	epMngrPromise promise.Promise[*endpointmanager.EndpointManager],
) *namespaceWatcher {

	w := &namespaceWatcher{}
	ctx, cancel := context.WithCancel(context.Background())

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			w.epMngr = epMngrPromise.Await()
			nsResource.Observe(ctx, w.onEvent, func(err error) { cancel() })
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			return nil
		}})
	return w
}

func (w *namespaceWatcher) onEvent(event k8s_resources.Event[*slim_corev1.Namespace]) {
	event.Dispatch(
		func(_ k8s_resources.Store[*slim_corev1.Namespace]) {},
		func(ns *slim_corev1.Namespace) {
			newLabels := getNSLabels(ns)

			oldIdtyLabels, _ := labelsfilter.Filter(w.oldLabels[ns.Name])
			newIdtyLabels, _ := labelsfilter.Filter(newLabels)

			// Do not perform any other operations the the old labels are the same as
			// the new labels
			if oldIdtyLabels.DeepEqual(&newIdtyLabels) {
				return
			}

			w.oldLabels[ns.Name] = newLabels

			eps := w.epMngr.GetEndpoints()
			for _, ep := range eps {
				epNS := ep.GetK8sNamespace()
				if ns.Name == epNS {
					err := ep.ModifyIdentityLabels(newIdtyLabels, oldIdtyLabels)
					if err != nil {
						log.WithError(err).WithField(logfields.EndpointID, ep.ID).
							Warningf("unable to update endpoint with new namespace labels")
					}
				}
			}
		},
		func(key k8s_resources.Key) {
			delete(w.oldLabels, key.Name)
		},
	)
}

func getNSLabels(ns *slim_corev1.Namespace) labels.Labels {
	labelMap := map[string]string{}
	for k, v := range ns.GetLabels() {
		labelMap[policy.JoinPath(ciliumio.PodNamespaceMetaLabels, k)] = v
	}
	return labels.Map2Labels(labelMap, labels.LabelSourceK8s)
}

func (k *K8sWatcher) namespacesInit(slimClient slimclientset.Interface, asyncControllers *sync.WaitGroup) {
	apiGroup := k8sAPIGroupNamespaceV1Core
	namespaceStore, namespaceController := informer.NewInformer(
		utils.ListerWatcherFromTyped[*slim_corev1.NamespaceList](slimClient.CoreV1().Namespaces()),
		&slim_corev1.Namespace{},
		0,
		cache.ResourceEventHandlerFuncs{},
		nil,
	)

	k.namespaceStore = namespaceStore
	k.blockWaitGroupToSyncResources(k.stop, nil, namespaceController.HasSynced, k8sAPIGroupNamespaceV1Core)
	k.k8sAPIGroups.AddAPI(apiGroup)
	asyncControllers.Done()
	namespaceController.Run(k.stop)
}

// GetCachedNamespace returns a namespace from the local store.
func (k *K8sWatcher) GetCachedNamespace(namespace string) (*slim_corev1.Namespace, error) {
	<-k.controllersStarted
	k.WaitForCacheSync(k8sAPIGroupNamespaceV1Core)
	nsName := &slim_corev1.Namespace{
		ObjectMeta: slim_metav1.ObjectMeta{
			Name: namespace,
		},
	}
	namespaceInterface, exists, err := k.namespaceStore.Get(nsName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, k8s_errors.NewNotFound(schema.GroupResource{
			Group:    "core",
			Resource: "namespace",
		}, namespace)
	}
	return namespaceInterface.(*slim_corev1.Namespace).DeepCopy(), nil
}
