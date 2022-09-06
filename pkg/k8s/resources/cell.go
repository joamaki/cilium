package resources

import (
	"context"
	"errors"
	"sync"

	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/cilium/cilium/pkg/hive"
	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	cilium_v2a1 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2alpha1"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_discover_v1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1"
	slim_networking_v1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/networking/v1"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/stream"
	"go.uber.org/fx"
)

var Cell = hive.NewCell(
	"k8s-resources",

	fx.Provide(resources),
)

type Resources struct {
	fx.Out

	Pods            stream.Observable[Event[*slim_corev1.Pod]]
	Nodes           stream.Observable[Event[*slim_corev1.Node]]
	Services        stream.Observable[Event[*slim_corev1.Service]]
	Namespaces      stream.Observable[Event[*slim_corev1.Namespace]]
	EndpointSlices  stream.Observable[Event[*slim_discover_v1.EndpointSlice]]
	NetworkPolicies stream.Observable[Event[*slim_networking_v1.NetworkPolicy]]

	CiliumNodes                      stream.Observable[Event[*cilium_v2.CiliumNode]]
	CiliumEndpoints                  stream.Observable[Event[*cilium_v2.CiliumEndpoint]]
	CiliumEnvoyConfigs               stream.Observable[Event[*cilium_v2.CiliumEnvoyConfig]]
	CiliumEndpointSlices             stream.Observable[Event[*cilium_v2a1.CiliumEndpointSlice]]
	CiliumNetworkPolicies            stream.Observable[Event[*cilium_v2.CiliumNetworkPolicy]]
	CiliumLocalRedirectPolicies      stream.Observable[Event[*cilium_v2.CiliumLocalRedirectPolicy]]
	CiliumEgressGatewayPolicies      stream.Observable[Event[*cilium_v2.CiliumEgressGatewayPolicy]]
	CiliumClusterwideEnvoyConfigs    stream.Observable[Event[*cilium_v2.CiliumClusterwideEnvoyConfig]]
	CiliumClusterwideNetworkPolicies stream.Observable[Event[*cilium_v2.CiliumClusterwideNetworkPolicy]]
}

func resources(lc fx.Lifecycle, c k8sClient.Clientset) (Resources, error) {
	var r Resources
	if !c.IsEnabled() {
		// TODO figure out what we should provide when there's no k8s client
		// configured. Empty stream? Error stream? Stuck stream?
		// Should use-sites declare these as optional and check if it's nil?
		return r, errors.New("Kubernetes client not enabled")
	}

	r.Pods = newResourceWithLifecycle[*slim_corev1.Pod](
		lc, utils.ListerWatcherFromTyped[*slim_corev1.PodList](c.Slim().CoreV1().Pods("")))
	r.Nodes = newResourceWithLifecycle[*slim_corev1.Node](
		lc, utils.ListerWatcherFromTyped[*slim_corev1.NodeList](c.Slim().CoreV1().Nodes()))
	r.Services = newResourceWithLifecycle[*slim_corev1.Service](
		lc, utils.ListerWatcherFromTyped[*slim_corev1.ServiceList](c.Slim().CoreV1().Services("")))
	r.Namespaces = newResourceWithLifecycle[*slim_corev1.Namespace](
		lc, utils.ListerWatcherFromTyped[*slim_corev1.NamespaceList](c.Slim().CoreV1().Namespaces()))
	r.EndpointSlices = newResourceWithLifecycle[*slim_discover_v1.EndpointSlice](
		lc, utils.ListerWatcherFromTyped[*slim_discover_v1.EndpointSliceList](c.Slim().DiscoveryV1().EndpointSlices("")))
	r.NetworkPolicies = newResourceWithLifecycle[*slim_networking_v1.NetworkPolicy](
		lc, utils.ListerWatcherFromTyped[*slim_networking_v1.NetworkPolicyList](c.Slim().NetworkingV1().NetworkPolicies("")))

	r.CiliumNodes = newResourceWithLifecycle[*cilium_v2.CiliumNode](
		lc, utils.ListerWatcherFromTyped[*cilium_v2.CiliumNodeList](c.CiliumV2().CiliumNodes()))
	r.CiliumEndpoints = newResourceWithLifecycle[*cilium_v2.CiliumEndpoint](
		lc, utils.ListerWatcherFromTyped[*cilium_v2.CiliumEndpointList](c.CiliumV2().CiliumEndpoints("")))
	r.CiliumEnvoyConfigs = newResourceWithLifecycle[*cilium_v2.CiliumEnvoyConfig](
		lc, utils.ListerWatcherFromTyped[*cilium_v2.CiliumEnvoyConfigList](c.CiliumV2().CiliumEnvoyConfigs("")))
	r.CiliumEndpointSlices = newResourceWithLifecycle[*cilium_v2a1.CiliumEndpointSlice](
		lc, utils.ListerWatcherFromTyped[*cilium_v2a1.CiliumEndpointSliceList](c.CiliumV2alpha1().CiliumEndpointSlices()))
	r.CiliumNetworkPolicies = newResourceWithLifecycle[*cilium_v2.CiliumNetworkPolicy](
		lc, utils.ListerWatcherFromTyped[*cilium_v2.CiliumNetworkPolicyList](c.CiliumV2().CiliumNetworkPolicies("")))
	r.CiliumLocalRedirectPolicies = newResourceWithLifecycle[*cilium_v2.CiliumLocalRedirectPolicy](
		lc, utils.ListerWatcherFromTyped[*cilium_v2.CiliumLocalRedirectPolicyList](c.CiliumV2().CiliumLocalRedirectPolicies("")))
	r.CiliumEgressGatewayPolicies = newResourceWithLifecycle[*cilium_v2.CiliumEgressGatewayPolicy](
		lc, utils.ListerWatcherFromTyped[*cilium_v2.CiliumEgressGatewayPolicyList](c.CiliumV2().CiliumEgressGatewayPolicies()))
	r.CiliumClusterwideEnvoyConfigs = newResourceWithLifecycle[*cilium_v2.CiliumClusterwideEnvoyConfig](
		lc, utils.ListerWatcherFromTyped[*cilium_v2.CiliumClusterwideEnvoyConfigList](c.CiliumV2().CiliumClusterwideEnvoyConfigs()))
	r.CiliumClusterwideNetworkPolicies = newResourceWithLifecycle[*cilium_v2.CiliumClusterwideNetworkPolicy](
		lc, utils.ListerWatcherFromTyped[*cilium_v2.CiliumClusterwideNetworkPolicyList](c.CiliumV2().CiliumClusterwideNetworkPolicies()))

	return r, nil
}

func newResourceWithLifecycle[T k8sRuntime.Object](lc fx.Lifecycle, lw cache.ListerWatcher) stream.Observable[Event[T]] {
	ctx, cancel := context.WithCancel(context.Background())
	var (
		run func()
		wg  sync.WaitGroup
	)

	src, run := NewResource[T](ctx, lw)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			wg.Add(1)
			go func() {
				defer wg.Done()
				run()
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			wg.Wait()
			return nil
		},
	})
	return src
}
