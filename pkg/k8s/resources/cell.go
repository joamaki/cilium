package resources

import (
	"k8s.io/client-go/tools/cache"

	"github.com/cilium/cilium/pkg/hive"
	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	cilium_v2a1 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2alpha1"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_discover_v1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1"
	slim_networking_v1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/networking/v1"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"go.uber.org/fx"
)

var Cell = hive.NewCell(
	"k8s-resources",

	fx.Provide(
		NewResourceConstructor[*slim_corev1.Pod](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_corev1.PodList](c.Slim().CoreV1().Pods(""))
			}),
		NewResourceConstructor[*slim_corev1.Node](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_corev1.NodeList](c.Slim().CoreV1().Nodes())
			}),
		NewResourceConstructor[*slim_corev1.Service](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_corev1.ServiceList](c.Slim().CoreV1().Services(""))
			}),
		NewResourceConstructor[*slim_corev1.Namespace](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_corev1.NamespaceList](c.Slim().CoreV1().Namespaces())
			}),
		NewResourceConstructor[*slim_discover_v1.EndpointSlice](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_discover_v1.EndpointSliceList](c.Slim().DiscoveryV1().EndpointSlices(""))
			}),
		NewResourceConstructor[*slim_networking_v1.NetworkPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*slim_networking_v1.NetworkPolicyList](c.Slim().NetworkingV1().NetworkPolicies(""))
			}),

		NewResourceConstructor[*cilium_v2.CiliumNode](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumNodeList](c.CiliumV2().CiliumNodes())
			}),
		NewResourceConstructor[*cilium_v2.CiliumEndpoint](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumEndpointList](c.CiliumV2().CiliumEndpoints(""))
			}),
		NewResourceConstructor[*cilium_v2.CiliumEnvoyConfig](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumEnvoyConfigList](c.CiliumV2().CiliumEnvoyConfigs(""))
			}),
		NewResourceConstructor[*cilium_v2a1.CiliumEndpointSlice](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2a1.CiliumEndpointSliceList](c.CiliumV2alpha1().CiliumEndpointSlices())
			}),
		NewResourceConstructor[*cilium_v2.CiliumNetworkPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumNetworkPolicyList](c.CiliumV2().CiliumNetworkPolicies(""))
			}),
		NewResourceConstructor[*cilium_v2.CiliumLocalRedirectPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumLocalRedirectPolicyList](c.CiliumV2().CiliumLocalRedirectPolicies(""))
			}),
		NewResourceConstructor[*cilium_v2.CiliumEgressGatewayPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumEgressGatewayPolicyList](c.CiliumV2().CiliumEgressGatewayPolicies())
			}),
		NewResourceConstructor[*cilium_v2.CiliumClusterwideEnvoyConfig](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumClusterwideEnvoyConfigList](c.CiliumV2().CiliumClusterwideEnvoyConfigs())
			}),
		NewResourceConstructor[*cilium_v2.CiliumClusterwideNetworkPolicy](
			func(c k8sClient.Clientset) cache.ListerWatcher {
				return utils.ListerWatcherFromTyped[*cilium_v2.CiliumClusterwideNetworkPolicyList](c.CiliumV2().CiliumClusterwideNetworkPolicies())
			}),
	),
)
