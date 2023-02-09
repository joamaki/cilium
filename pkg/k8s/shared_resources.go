// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package k8s

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	cilium_v2alpha1 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2alpha1"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_discoveryv1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1"
	slim_discoveryv1beta1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1beta1"
	"github.com/cilium/cilium/pkg/k8s/utils"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
)

var (
	// SharedResourceCell provides a set of shared handles to Kubernetes resources used throughout the
	// Cilium agent. Each of the resources share a client-go informer and backing store so we only
	// have one watch API call for each resource kind and that we maintain only one copy of each object.
	//
	// See pkg/k8s/resource/resource.go for documentation on the Resource[T] type.
	SharedResourcesCell = cell.Module(
		"k8s-shared-resources",
		"Shared Kubernetes resources",

		cell.Provide(
			serviceResource,
			localNodeResource,
			localCiliumNodeResource,
			namespaceResource,
			lbIPPoolsResource,
			ciliumIdentityResource,
			endpointsResource,
			cecResource,
			ccecResource,
			lrpResource,
			podResource,
		),

		cell.Invoke(periodicStatusReporter),
	)
)

type SharedResources struct {
	cell.In

	LocalNode                     LocalNodeResource
	LocalCiliumNode               LocalCiliumNodeResource
	Services                      resource.Resource[*slim_corev1.Service]
	Namespaces                    resource.Resource[*slim_corev1.Namespace]
	LBIPPools                     resource.Resource[*cilium_v2alpha1.CiliumLoadBalancerIPPool]
	Identities                    resource.Resource[*cilium_v2.CiliumIdentity]
	Endpoints                     resource.Resource[*Endpoints]
	CiliumEnvoyConfigs            resource.Resource[*cilium_v2.CiliumEnvoyConfig]
	CiliumClusterwideEnvoyConfigs resource.Resource[*cilium_v2.CiliumClusterwideEnvoyConfig]
	CiliumLocalRedirectPolicies   resource.Resource[*cilium_v2.CiliumLocalRedirectPolicy]
	Pods                          resource.Resource[*slim_corev1.Pod]
}

func periodicStatusReporter(lc hive.Lifecycle, resources SharedResources, reporter cell.StatusReporter) {
	t := time.NewTicker(10 * time.Second)
	lc.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			go func() {
				lastStatus := ""
				lastStatus = reportStatus(lastStatus, resources, reporter)
				for range t.C {
					lastStatus = reportStatus(lastStatus, resources, reporter)
				}
			}()
			return nil
		},
		OnStop: func(hive.HookContext) error {
			t.Stop()
			return nil
		},
	})
}

func reportStatus(lastStatus string, resources SharedResources, reporter cell.StatusReporter) string {
	type statusGetter interface {
		Status() (string, bool)
	}
	nominal := true
	statuses := []string{}

	appendStatus := func(g statusGetter) {
		status, ok := g.Status()
		nominal = nominal && ok
		// TODO pretty names?
		if status != "" {
			name := fmt.Sprintf("%T", g)
			// TODO fix this hack. maybe give resource a name by hand?
			name = strings.TrimRight(name, "]")
			sepIdx := strings.LastIndexAny(name, "/.")
			if sepIdx >= 0 {
				name = name[sepIdx+1:]
			}
			statuses = append(statuses, fmt.Sprintf("%s: %s", name, status))
		}
	}

	v := reflect.ValueOf(resources)
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.IsZero() {
			continue
		}
		g, ok := f.Interface().(statusGetter)
		if ok {
			appendStatus(g)
		}
	}

	status := ""
	if len(statuses) == 0 {
		status = "No resources needed"
	} else {
		status = strings.Join(statuses, ", ")
	}

	if status == lastStatus {
		return status
	}

	if nominal {
		reporter.OK(status)
	} else {
		// TODO only report the degraded status?
		reporter.Degraded(status)
	}
	return status
}

func serviceResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*slim_corev1.Service], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	optsModifier, err := utils.GetServiceListOptionsModifier(option.Config)
	if err != nil {
		return nil, err
	}
	lw := utils.ListerWatcherFromTyped[*slim_corev1.ServiceList](cs.Slim().CoreV1().Services(""))
	lw = utils.ListerWatcherWithModifier(lw, optsModifier)
	return resource.New[*slim_corev1.Service](lc, lw), nil
}

// LocalNodeResource is a resource.Resource[*corev1.Node] but one which will only stream updates for the node object
// associated with the node we are currently running on.
type LocalNodeResource resource.Resource[*corev1.Node]

func localNodeResource(lc hive.Lifecycle, cs client.Clientset) (LocalNodeResource, error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*corev1.NodeList](cs.CoreV1().Nodes())
	lw = utils.ListerWatcherWithFields(lw, fields.ParseSelectorOrDie("metadata.name="+nodeTypes.GetName()))
	return LocalNodeResource(resource.New[*corev1.Node](lc, lw)), nil
}

// LocalCiliumNodeResource is a resource.Resource[*cilium_v2.Node] but one which will only stream updates for the
// CiliumNode object associated with the node we are currently running on.
type LocalCiliumNodeResource resource.Resource[*cilium_v2.CiliumNode]

func localCiliumNodeResource(lc hive.Lifecycle, cs client.Clientset) (LocalCiliumNodeResource, error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*cilium_v2.CiliumNodeList](cs.CiliumV2().CiliumNodes())
	lw = utils.ListerWatcherWithFields(lw, fields.ParseSelectorOrDie("metadata.name="+nodeTypes.GetName()))
	return LocalCiliumNodeResource(resource.New[*cilium_v2.CiliumNode](lc, lw)), nil
}

func namespaceResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*slim_corev1.Namespace], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*slim_corev1.NamespaceList](cs.Slim().CoreV1().Namespaces())
	return resource.New[*slim_corev1.Namespace](lc, lw), nil
}

func lbIPPoolsResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*cilium_v2alpha1.CiliumLoadBalancerIPPool], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*cilium_v2alpha1.CiliumLoadBalancerIPPoolList](
		cs.CiliumV2alpha1().CiliumLoadBalancerIPPools(),
	)
	return resource.New[*cilium_v2alpha1.CiliumLoadBalancerIPPool](lc, lw), nil
}

func ciliumIdentityResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*cilium_v2.CiliumIdentity], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*cilium_v2.CiliumIdentityList](
		cs.CiliumV2().CiliumIdentities(),
	)
	return resource.New[*cilium_v2.CiliumIdentity](lc, lw), nil
}

// endpointsListerWatcher implements List and Watch for endpoints/endpointslices. It
// performs the capability check on first call to List/Watch. This allows constructing
// the resource before the client has been started and capabilities have been probed.
type endpointsListerWatcher struct {
	cs client.Clientset

	once                sync.Once
	cachedListerWatcher cache.ListerWatcher
}

func (lw *endpointsListerWatcher) getListerWatcher() cache.ListerWatcher {
	lw.once.Do(func() {
		if SupportsEndpointSlice() {
			if SupportsEndpointSliceV1() {
				log.Infof("Using discoveryv1.EndpointSlice")
				lw.cachedListerWatcher = utils.ListerWatcherFromTyped[*slim_discoveryv1.EndpointSliceList](
					lw.cs.Slim().DiscoveryV1().EndpointSlices(""),
				)
			} else {
				log.Infof("Using discoveryv1beta1.EndpointSlice")
				lw.cachedListerWatcher = utils.ListerWatcherFromTyped[*slim_discoveryv1beta1.EndpointSliceList](
					lw.cs.Slim().DiscoveryV1beta1().EndpointSlices(""),
				)
			}
		} else {
			log.Infof("Using v1.Endpoints")
			lw.cachedListerWatcher = utils.ListerWatcherFromTyped[*slim_corev1.EndpointsList](
				lw.cs.Slim().CoreV1().Endpoints(""),
			)
		}
	})
	return lw.cachedListerWatcher
}

func (lw *endpointsListerWatcher) List(opts metav1.ListOptions) (k8sRuntime.Object, error) {
	return lw.getListerWatcher().List(opts)
}

func (lw *endpointsListerWatcher) Watch(opts metav1.ListOptions) (watch.Interface, error) {
	return lw.getListerWatcher().Watch(opts)
}

func transformEndpoint(obj any) (any, error) {
	switch obj := obj.(type) {
	case *slim_corev1.Endpoints:
		_, eps := ParseEndpoints(obj)
		return eps, nil
	case *slim_discoveryv1.EndpointSlice:
		_, eps := ParseEndpointSliceV1(obj)
		return eps, nil
	case *slim_discoveryv1beta1.EndpointSlice:
		_, eps := ParseEndpointSliceV1Beta1(obj)
		return eps, nil
	default:
		return nil, fmt.Errorf("%T not a known endpoint or endpoint slice object", obj)
	}
}

func endpointsResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*Endpoints], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	return resource.New[*Endpoints](
		lc,
		&endpointsListerWatcher{cs: cs},
		resource.WithTransform(transformEndpoint),
	), nil
}

func cecResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*cilium_v2.CiliumEnvoyConfig], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*cilium_v2.CiliumEnvoyConfigList](cs.CiliumV2().CiliumEnvoyConfigs(""))
	return resource.New[*cilium_v2.CiliumEnvoyConfig](lc, lw), nil
}

func ccecResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*cilium_v2.CiliumClusterwideEnvoyConfig], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*cilium_v2.CiliumClusterwideEnvoyConfigList](cs.CiliumV2().CiliumClusterwideEnvoyConfigs())
	return resource.New[*cilium_v2.CiliumClusterwideEnvoyConfig](lc, lw), nil
}

func lrpResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*cilium_v2.CiliumLocalRedirectPolicy], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*cilium_v2.CiliumLocalRedirectPolicyList](cs.CiliumV2().CiliumLocalRedirectPolicies(""))
	return resource.New[*cilium_v2.CiliumLocalRedirectPolicy](lc, lw), nil
}

func podResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*slim_corev1.Pod], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*slim_corev1.PodList](cs.Slim().CoreV1().Pods(""))
	return resource.New[*slim_corev1.Pod](lc, lw), nil
}
