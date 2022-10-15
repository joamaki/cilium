// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package watchers

import (
	"context"

	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/k8s/watchers/resources"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/option"
)

func (k *K8sWatcher) servicesInit() {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-k.stop
		cancel()
	}()

	apiGroup := resources.K8sAPIGroupServiceV1Core
	synced := false
	swgSvcs := lock.NewStoppableWaitGroup()

	k.blockWaitGroupToSyncResources(
		k.stop,
		swgSvcs,
		func() bool { return synced },
		resources.K8sAPIGroupServiceV1Core,
	)

	k.sharedResources.Services.Observe(
		ctx,
		func(ev resource.Event[*slim_corev1.Service]) {
			ev.Handle(
				func() error {
					synced = true
					return nil
				},
				func(_ resource.Key, svc *slim_corev1.Service) error {
					k.K8sEventReceived(apiGroup, resources.MetricService, resources.MetricUpdate, true, false)
					err := k.upsertK8sServiceV1(svc, swgSvcs)
					k.K8sEventProcessed(resources.MetricService, resources.MetricUpdate, err == nil)
					return nil
				},
				func(_ resource.Key, svc *slim_corev1.Service) error {
					k.K8sEventReceived(apiGroup, resources.MetricService, resources.MetricUpdate, true, false)
					err := k.deleteK8sServiceV1(svc, swgSvcs)
					k.K8sEventProcessed(resources.MetricService, resources.MetricDelete, err == nil)
					return nil
				},
			)

		},
		nil, // completion means we're shutting down
	)

	k.k8sAPIGroups.AddAPI(apiGroup)
}

func (k *K8sWatcher) upsertK8sServiceV1(svc *slim_corev1.Service, swg *lock.StoppableWaitGroup) error {
	svcID := k.K8sSvcCache.UpdateService(svc, swg)
	if option.Config.EnableLocalRedirectPolicy {
		if svc.Spec.Type == slim_corev1.ServiceTypeClusterIP {
			// The local redirect policies currently support services of type
			// clusterIP only.
			k.redirectPolicyManager.OnAddService(svcID)
		}
	}
	if option.Config.BGPAnnounceLBIP {
		k.bgpSpeakerManager.OnUpdateService(svc)
	}
	return nil
}

func (k *K8sWatcher) deleteK8sServiceV1(svc *slim_corev1.Service, swg *lock.StoppableWaitGroup) error {
	k.K8sSvcCache.DeleteService(svc, swg)
	svcID := k8s.ParseServiceID(svc)
	if option.Config.EnableLocalRedirectPolicy {
		if svc.Spec.Type == slim_corev1.ServiceTypeClusterIP {
			k.redirectPolicyManager.OnDeleteService(svcID)
		}
	}
	if option.Config.BGPAnnounceLBIP {
		k.bgpSpeakerManager.OnDeleteService(svc)
	}
	return nil
}
