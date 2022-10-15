// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package watchers

import (
	"context"
	"errors"
	"sync"

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	ciliumio "github.com/cilium/cilium/pkg/k8s/apis/cilium.io"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_metav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	"github.com/cilium/cilium/pkg/k8s/watchers/resources"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/labelsfilter"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/policy"
)

func (k *K8sWatcher) namespacesInit(asyncControllers *sync.WaitGroup) {
	apiGroup := k8sAPIGroupNamespaceV1Core

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
	k.k8sAPIGroups.AddAPI(apiGroup)

	nsUpdater := namespaceUpdater{
		oldLabels:       make(map[string]labels.Labels),
		endpointManager: k.endpointManager,
	}

	k.sharedResources.Namespaces.Observe(
		ctx,
		func(ev resource.Event[*slim_corev1.Namespace]) {
			ev.Handle(
				func() error {
					synced = true
					return nil
				},
				func(key resource.Key, newNS *slim_corev1.Namespace) error {
					err := nsUpdater.update(newNS)
					k.K8sEventProcessed(metricNS, resources.MetricUpdate, err == nil)
					// Return the error to retry the endpoint label updates at a later time.
					return err
				},
				nil,
			)
		},
		nil, // completion means we're shutting down
	)

	asyncControllers.Done()
}

type namespaceUpdater struct {
	oldLabels map[string]labels.Labels

	endpointManager endpointManager
}

func getNamespaceLabels(ns *slim_corev1.Namespace) labels.Labels {
	labelMap := map[string]string{}
	for k, v := range ns.GetLabels() {
		labelMap[policy.JoinPath(ciliumio.PodNamespaceMetaLabels, k)] = v
	}
	return labels.Map2Labels(labelMap, labels.LabelSourceK8s)
}

func (u *namespaceUpdater) update(newNS *slim_corev1.Namespace) error {
	oldLabels := u.oldLabels[newNS.Name]
	newLabels := getNamespaceLabels(newNS)

	oldIdtyLabels, _ := labelsfilter.Filter(oldLabels)
	newIdtyLabels, _ := labelsfilter.Filter(newLabels)

	// Do not perform any other operations the the old labels are the same as
	// the new labels
	if oldIdtyLabels.DeepEqual(&newIdtyLabels) {
		return nil
	}

	eps := u.endpointManager.GetEndpoints()
	failed := false
	for _, ep := range eps {
		epNS := ep.GetK8sNamespace()
		if newNS.Name == epNS {
			err := ep.ModifyIdentityLabels(newIdtyLabels, oldIdtyLabels)
			if err != nil {
				log.WithError(err).WithField(logfields.EndpointID, ep.ID).
					Warningf("unable to update endpoint with new namespace labels")
				failed = true
			}
		}
	}
	if failed {
		return errors.New("unable to update some endpoints with new namespace labels")
	}
	return nil
}

// GetCachedNamespace returns a namespace from the local store.
func (k *K8sWatcher) GetCachedNamespace(namespace string) (*slim_corev1.Namespace, error) {
	nsName := &slim_corev1.Namespace{
		ObjectMeta: slim_metav1.ObjectMeta{
			Name: namespace,
		},
	}

	store, err := k.sharedResources.Namespaces.Store(context.Background())
	if err != nil {
		return nil, err
	}
	ns, exists, err := store.Get(nsName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, k8s_errors.NewNotFound(schema.GroupResource{
			Group:    "core",
			Resource: "namespace",
		}, namespace)
	}
	return ns.DeepCopy(), nil
}
