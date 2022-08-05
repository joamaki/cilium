// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package k8s

import (
	"context"
	"fmt"

	k8sSource "github.com/joamaki/goreactive/sources/k8s"
	"github.com/joamaki/goreactive/stream"
	"go.uber.org/fx"
	"k8s.io/client-go/rest"

	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	ciliumclientset "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_discoveryv1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1"
	apiextclientset "github.com/cilium/cilium/pkg/k8s/slim/k8s/apiextensions-clientset"
	slimclientset "github.com/cilium/cilium/pkg/k8s/slim/k8s/client/clientset/versioned"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/source"
)

type ClientConfig struct {
	APIServerURL string   `flag:"k8s-api-server"      usage:"K8s API Server URL"`
	KubeConfigPath string `flag:"k8s-kubeconfig-path" usage:"K8s kubeconfig path"`
	QPS float32           `flag:"k8s-client-qps"      usage:"K8s client queries per second limit"`
	Burst int             `flag:"k8s-client-burst"    usage:"K8s client burst limit"`
}

var defaultClientConfig = ClientConfig{"", "", 100, 5}

var ClientModule = fx.Module(
	"k8s-client",

	option.ConfigOption(defaultClientConfig),
	fx.Provide(k8sClient),
)

// Type aliases for the clientsets to avoid name collision on 'Clientset' when composing them.
// NOTE: Still collision with Discovery() between SlimClientset and CiliumClientset, so that needs
// to be disambiguated at use site.
type (
	SlimClientset = slimclientset.Clientset
	APIExtClientset = apiextclientset.Clientset
	CiliumClientset = ciliumclientset.Clientset
)

// Clientset is a composition of the different client sets used by Cilium.
type Clientset struct {
	*SlimClientset
	*APIExtClientset
	*CiliumClientset
}

func k8sClient(lc fx.Lifecycle, config ClientConfig) (*Clientset, error) {
	var client Clientset

	restConfig, err := createConfig(config.APIServerURL, config.KubeConfigPath, config.QPS, config.Burst)
	if err != nil {
		return nil, fmt.Errorf("unable to create k8s client rest configuration: %w", err)
	}
	closeAllConns := setDialer(restConfig)
	
	httpClient, err := rest.HTTPClientFor(restConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to create k8s REST client: %w", err)
	}

	// TODO(JM): Why is this set only after the http client is created? Is it a problem
	// that we're mutating it after http client is created?
	restConfig.ContentConfig.ContentType = `application/vnd.kubernetes.protobuf`

	client.SlimClientset, err = slimclientset.NewForConfigAndClient(restConfig, httpClient)
	if err != nil {
		return nil, fmt.Errorf("unable to create slim k8s client: %w", err)
	}

	client.APIExtClientset, err = apiextclientset.NewForConfigAndClient(restConfig, httpClient)
	if err != nil {
		return nil, fmt.Errorf("unable to create apiext k8s client: %w", err)
	}

	client.CiliumClientset, err = ciliumclientset.NewForConfigAndClient(restConfig, httpClient)
	if err != nil {
		return nil, fmt.Errorf("unable to create cilium k8s client: %w", err)
	}

	// TODO: Add in the bits needed from the core client that don't overlap with slim client.

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			return waitUntilConnReady(restConfig, client.SlimClientset)
		},
		OnStop: func(context.Context) error {
			// TODO: need the DISABLE_HTTP2 workaround? See Init().
			closeAllConns()
			return nil
		},
	})

	return &client, nil
}

var ResourcesModule = fx.Module(
	"k8s-resources",
	fx.Provide(k8sResources, k8sLocalNodeInformation),
)

// Type aliases & wrappres for the observable and event types to make them less unwieldy at
// use sites and in the dependency graph.
// 
// Not totally sure we want the wrappers since they mess with type inference, e.g. to use
// the map operator with a NodesObservable one needs the type annotation.
type (
	Key = k8sSource.Key

	NodesEvent = k8sSource.Event[*slim_corev1.Node]
	NodesObservable struct { stream.Observable[NodesEvent] }

	ServicesEvent = k8sSource.Event[*slim_corev1.Service]
	ServicesObservable struct { stream.Observable[ServicesEvent] }

	// TODO: Implement an adapter for discovery v1beta1 that converts to v1.
	EndpointSlicesEvent = k8sSource.Event[*slim_discoveryv1.EndpointSlice]
	EndpointSlicesObservable struct { stream.Observable[EndpointSlicesEvent] }

	CiliumEndpointsEvent = k8sSource.Event[*cilium_v2.CiliumEndpoint]
	CiliumEndpointsObservable struct { stream.Observable[CiliumEndpointsEvent] }
)

type K8sResources struct {
	fx.Out

	Nodes NodesObservable
	Services ServicesObservable
	EndpointSlices EndpointSlicesObservable
	CiliumEndpoints CiliumEndpointsObservable
}

func k8sResources(lc fx.Lifecycle, client *Clientset) K8sResources {
	ctx, cancel := context.WithCancel(context.Background())

	// Create deferred streams that will be started from the OnStart hook.
	// TODO: consider making the resource observable cold by default.
	nodes, startNodes := stream.Deferred[NodesEvent]()
	services, startServices := stream.Deferred[ServicesEvent]()
	endpointSlices, startEndpointSlices := stream.Deferred[EndpointSlicesEvent]()
	ciliumEndpoints, startCE := stream.Deferred[CiliumEndpointsEvent]()

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			startNodes(
				k8sSource.NewResourceFromListWatch[*slim_corev1.Node, *slim_corev1.NodeList](
					ctx, client.CoreV1().Nodes()))
			startServices(
				k8sSource.NewResourceFromListWatch[*slim_corev1.Service, *slim_corev1.ServiceList](
					ctx, client.CoreV1().Services("")))
			startEndpointSlices(
				k8sSource.NewResourceFromListWatch[*slim_discoveryv1.EndpointSlice, *slim_discoveryv1.EndpointSliceList](
					ctx, client.DiscoveryV1().EndpointSlices("")))
			startCE(
				k8sSource.NewResourceFromListWatch[*cilium_v2.CiliumEndpoint, *cilium_v2.CiliumEndpointList](
					ctx, client.CiliumV2().CiliumEndpoints("")))
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			// TODO: we should wait for the informers to exit from Run().
			return nil
		},
	})

	return K8sResources{
		Nodes: NodesObservable{nodes},
		Services: ServicesObservable{services},
		EndpointSlices: EndpointSlicesObservable{endpointSlices},
		CiliumEndpoints: CiliumEndpointsObservable{ciliumEndpoints},
	}
}

type NodeInformationObservable struct {
	stream.Observable[NodeInformation]
}

type NodeInformation struct {
	*nodeTypes.Node
}

func parseNodeInfo(node *slim_corev1.Node) NodeInformation {
	return NodeInformation{ParseNode(node, source.Unspec)}
}

func k8sLocalNodeInformation(nodes NodesObservable) NodeInformationObservable {
	// Only look at the updates for the local node.
	localNodeUpdates := stream.FlatMap[NodesEvent](nodes, func(event NodesEvent) stream.Observable[*slim_corev1.Node] {
		if update, ok := event.(*k8sSource.UpdateEvent[*slim_corev1.Node]); ok {
			// TODO filter local node: if update.Key.Name == nodeName {
			return stream.Just(update.Object)
		}
		return stream.Empty[*slim_corev1.Node]()
	})

	// Parse the local node updates into node information.
	nodeInformation := stream.Map(localNodeUpdates, parseNodeInfo)

	// TODO filter out duplicate entries...
	/*nodeInformation = stream.Flatten(
		stream.Scan(nodeInformation, NodeInformation{nil}, func(old, new NodeInformation) stream.Observable[NodeInformation] {
			if old == nil || old.DeepEqual(new) {
				return stream.Empty()
			}
			return stream.Just(new)
		}
	}))*/

	// Wrap with multicast for multiple subscribers.
	nodeInformation, connect := stream.Multicast(stream.MulticastParams{BufferSize: 1, EmitLatest: true}, nodeInformation)

	// TODO from start hook
	go connect(context.TODO())

	// TODO: Do we want a stream of changing node information, or should this just wait for the first version
	// and then provide a singleton stream for that (or an errored stream if it fails)?

	return NodeInformationObservable{nodeInformation}
}

