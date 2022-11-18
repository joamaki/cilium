// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package watchers

import (
	"context"

	"github.com/cilium/cilium/pkg/bgp/manager"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
)

// StartLBIPAllocator starts the service watcher if it hasn't already and looks
// for service of type LoadBalancer. Once it finds a service of that type, it
// will try to allocate an external IP (LoadBalancerIP) for it.
func StartLBIPAllocator(ctx context.Context, cfg ServiceSyncConfiguration, clientset k8sClient.Clientset, services resource.Resource[*slim_corev1.Service]) {
	store, err := services.Store(ctx)
	m, err := manager.New(ctx, clientset, store.CacheStore())
	if err != nil {
		log.WithError(err).Fatal("Error creating BGP manager")
	}
	serviceSubscribers.Register(m)
}
