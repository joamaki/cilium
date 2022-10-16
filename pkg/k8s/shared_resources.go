package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_discoveryv1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1"
	"github.com/cilium/cilium/pkg/k8s/utils"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
	"k8s.io/apimachinery/pkg/fields"
)

var (
	// SharedResourceCell provides a set of shared handles to Kubernetes resources used throughout the
	// Cilium agent. Each of the resources share a client-go informer and backing store so we only
	// have one watch API call for each resource kind and that we maintain only one copy of each object.
	//
	// If you need a filtered view to a resource (e.g. objects in certain namespace) and it already
	// has a more general resource, then use stream.Filter() to filter the event stream rather than
	// creating a new resource.
	//
	// See pkg/k8s/resource/resource.go for documentation on the Resource[T] type.
	SharedResourcesCell = cell.Module(
		"k8s-shared-resources",
		cell.Provide(
			serviceResource,
			localNodeResource,
			namespaceResource,
			//endpointsResourceFromEndpoints,
			endpointsResourceFromEndpointSlices,
		),

		cell.Invoke(testEndpointsResource),
	)
)

type SharedResources struct {
	cell.In
	LocalNode  resource.Resource[*corev1.Node]
	Services   resource.Resource[*slim_corev1.Service]
	Namespaces resource.Resource[*slim_corev1.Namespace]
	Endpoints  resource.Resource[*CommonEndpoints]
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

func localNodeResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*corev1.Node], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*corev1.NodeList](cs.CoreV1().Nodes())
	lw = utils.ListerWatcherWithFields(lw, fields.ParseSelectorOrDie("metadata.name="+nodeTypes.GetName()))
	return resource.New[*corev1.Node](lc, lw), nil
}

func namespaceResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*slim_corev1.Namespace], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*slim_corev1.NamespaceList](cs.Slim().CoreV1().Namespaces())
	return resource.New[*slim_corev1.Namespace](lc, lw), nil
}

func transformEndpoint(obj any) (any, error) {
	in, ok := obj.(*slim_corev1.Endpoints)
	if !ok {
		return nil, fmt.Errorf("%T not slim_corev1.Endpoints", obj)
	}
	svcID, eps := ParseEndpoints(in)
	c := &CommonEndpoints{
		// We keep the original ObjectMeta to hold onto the original name
		// and resource version.
		ObjectMeta:      in.ObjectMeta,
		EndpointSliceID: EndpointSliceID{ServiceID: svcID},
		Endpoints:       *eps,
	}
	return c, nil
}

func transformEndpointSlice(obj any) (any, error) {
	in, ok := obj.(*slim_discoveryv1.EndpointSlice)
	if !ok {
		return nil, fmt.Errorf("%T not slim_discoveryv1.EndpointSlice", obj)
	}
	id, eps := ParseEndpointSliceV1(in)
	c := &CommonEndpoints{
		// We keep the original ObjectMeta to hold onto the original name
		// and resource version.
		ObjectMeta:      in.ObjectMeta,
		EndpointSliceID: id,
		Endpoints:       *eps,
	}
	return c, nil
}

func endpointsResourceFromEndpoints(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*CommonEndpoints], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	// TODO figure out capabilities and use proper kind

	return resource.New[*CommonEndpoints](
		lc,
		utils.ListerWatcherFromTyped[*slim_corev1.EndpointsList](
			cs.Slim().CoreV1().Endpoints(""),
		),
		resource.WithTransform(transformEndpoint),
	), nil
}

func endpointsResourceFromEndpointSlices(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*CommonEndpoints], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	// TODO figure out capabilities and use proper kind

	return resource.New[*CommonEndpoints](
		lc,
		utils.ListerWatcherFromTyped[*slim_discoveryv1.EndpointSliceList](
			cs.Slim().DiscoveryV1().EndpointSlices(""),
		),
		resource.WithTransform(transformEndpointSlice),
	), nil
}

func testEndpointsResource(es resource.Resource[*CommonEndpoints]) {
	es.Observe(context.TODO(),
		func(ev resource.Event[*CommonEndpoints]) {
			ev.Handle(
				func() error {
					fmt.Printf(">>> synced!\n")
					return nil
				},
				func(_ resource.Key, es *CommonEndpoints) error {
					fmt.Printf(">>> %v\n", es)
					return nil
				},
				nil,
			)
		},
		nil)
}
