package resources

import (
	"context"
	"errors"
	"fmt"
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

	fx.Provide(
		newResourceWithLifecycle[*slim_corev1.Pod](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_corev1.PodList](c.Slim().CoreV1().Pods(""))
			}),
		newResourceWithLifecycle[*slim_corev1.Node](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_corev1.NodeList](c.Slim().CoreV1().Nodes())
			}),
		newResourceWithLifecycle[*slim_corev1.Service](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_corev1.ServiceList](c.Slim().CoreV1().Services(""))
			}),
		newResourceWithLifecycle[*slim_corev1.Namespace](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_corev1.NamespaceList](c.Slim().CoreV1().Namespaces())
			}),
		newResourceWithLifecycle[*slim_discover_v1.EndpointSlice](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_discover_v1.EndpointSliceList](c.Slim().DiscoveryV1().EndpointSlices(""))
			}),
		newResourceWithLifecycle[*slim_networking_v1.NetworkPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_networking_v1.NetworkPolicyList](c.Slim().NetworkingV1().NetworkPolicies(""))
			}),

		newResourceWithLifecycle[*cilium_v2.CiliumNode](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumNodeList](c.CiliumV2().CiliumNodes())
			}),
		newResourceWithLifecycle[*cilium_v2.CiliumEndpoint](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumEndpointList](c.CiliumV2().CiliumEndpoints(""))
			}),
		newResourceWithLifecycle[*cilium_v2.CiliumEnvoyConfig](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumEnvoyConfigList](c.CiliumV2().CiliumEnvoyConfigs(""))
			}),
		newResourceWithLifecycle[*cilium_v2a1.CiliumEndpointSlice](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2a1.CiliumEndpointSliceList](c.CiliumV2alpha1().CiliumEndpointSlices())
			}),
		newResourceWithLifecycle[*cilium_v2.CiliumNetworkPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumNetworkPolicyList](c.CiliumV2().CiliumNetworkPolicies(""))
			}),
		newResourceWithLifecycle[*cilium_v2.CiliumLocalRedirectPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumLocalRedirectPolicyList](c.CiliumV2().CiliumLocalRedirectPolicies(""))
			}),
		newResourceWithLifecycle[*cilium_v2.CiliumEgressGatewayPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumEgressGatewayPolicyList](c.CiliumV2().CiliumEgressGatewayPolicies())
			}),
		newResourceWithLifecycle[*cilium_v2.CiliumClusterwideEnvoyConfig](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumClusterwideEnvoyConfigList](c.CiliumV2().CiliumClusterwideEnvoyConfigs())
			}),
		newResourceWithLifecycle[*cilium_v2.CiliumClusterwideNetworkPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumClusterwideNetworkPolicyList](c.CiliumV2().CiliumClusterwideNetworkPolicies())
			}),
	),
)

func newResourceWithLifecycle[T k8sRuntime.Object](lw func(c k8sClient.Clientset) cache.ListerWatcher) func(lc fx.Lifecycle, c k8sClient.Clientset) stream.Observable[Event[T]] {
	return func(lc fx.Lifecycle, c k8sClient.Clientset) stream.Observable[Event[T]] {
		if !c.IsEnabled() {
			// TODO figure out what we should provide when there's no k8s client
			// configured. Empty stream? Error stream? Stuck stream?
			// Should use-sites declare these as optional and check if it's nil?
			return stream.Error[Event[T]](errors.New("Kubernetes client not enabled"))
		}

		ctx, cancel := context.WithCancel(context.Background())
		var (
			run func()
			wg  sync.WaitGroup
		)

		src, run := NewResource[T](ctx, lw(c))

		lc.Append(fx.Hook{
			OnStart: func(context.Context) error {
				var foo T
				fmt.Printf("started resource for type %T\n", foo)
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
}
