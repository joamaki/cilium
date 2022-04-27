package k8s

import (
	"context"
	"fmt"

	"go.uber.org/fx"
	v1 "k8s.io/api/core/v1"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"

	"github.com/cilium/cilium/pkg/eventsource"
	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/client/informers/externalversions"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type CiliumInformerFactory = externalversions.SharedInformerFactory
type K8sInformerFactory = informers.SharedInformerFactory

// SharedInformerModule builds and starts the informer factories for kubernetes and cilium.
var SharedInformerModule = fx.Module(
	"shared-informers",
	fx.Provide(
		func(lc fx.Lifecycle, k8sClient *K8sClient, ciliumClient *K8sCiliumClient) (K8sInformerFactory, CiliumInformerFactory) {
			k8sFactory := informers.NewSharedInformerFactory(k8sClient, 0)
			ciliumFactory := externalversions.NewSharedInformerFactory(ciliumClient, 0)

			stopCh := make(chan struct{})
			lc.Append(fx.Hook{
				  OnStart: func(context.Context) error {
					  k8sFactory.Start(stopCh)
					  ciliumFactory.Start(stopCh)
					  return nil
				  },
				  OnStop: func(context.Context) error {
					  close(stopCh)
					  return nil
				  },
			})
			return k8sFactory, ciliumFactory
		},
	),
)


type K8sKey struct {
	// Namespace is optional and not applicable to all resource types.
	Namespace string
	Name string
}

// K8sResource provides access to a K8s resource.
type K8sResource[T k8sRuntime.Object] interface {
	ChangedKeys() eventsource.EventSource[K8sKey]
	List(selector labels.Selector) (ret []T, err error)
	Get(key K8sKey) (T, error)
}

type k8sResourceHandle[T k8sRuntime.Object] struct {
	empty T
	source eventsource.EventSource[K8sKey]
	publish eventsource.PublishFunc[K8sKey]
	queue workqueue.RateLimitingInterface
	informer cache.SharedIndexInformer
	lister genericLister[T]
}

func objToKey(obj interface{}) (K8sKey, error) {
	// TODO: Saner key handling?
	k, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		return K8sKey{}, err
	}
	namespace, name, err := cache.SplitMetaNamespaceKey(k)
	return K8sKey{namespace, name}, err
}

// genericLister is similar to cache.GenericLister but over type T and
// works with namespaced and non-namespaced objects.
type genericLister[T k8sRuntime.Object] struct {
	Get func(key K8sKey) (T, error)
	List func(selector labels.Selector) ([]T, error)
	// TODO: namespaced List?
}

type informerAndLister[T k8sRuntime.Object] struct {
	informer cache.SharedIndexInformer
	lister genericLister[T]
}

type ResourceProvider[T k8sRuntime.Object] func(K8sInformerFactory, CiliumInformerFactory) informerAndLister[T]

func Nodes(factory informers.SharedInformerFactory, _ CiliumInformerFactory) informerAndLister[*v1.Node] {
	nodes := factory.Core().V1().Nodes()
	lister := nodes.Lister()
	return informerAndLister[*v1.Node]{
		nodes.Informer(),
		genericLister[*v1.Node]{
			List: lister.List,
			Get: func(key K8sKey) (*v1.Node, error) {
				return lister.Get(key.Name)
			},
		},
	}
}

func CiliumNode(_ K8sInformerFactory, factory CiliumInformerFactory) informerAndLister[*cilium_v2.CiliumNode] {
	informer := factory.Cilium().V2().CiliumNodes().Informer()
	lister := factory.Cilium().V2().CiliumNodes().Lister()
	return informerAndLister[*cilium_v2.CiliumNode]{
		informer,
		genericLister[*cilium_v2.CiliumNode]{
			List: lister.List,
			Get: func(key K8sKey) (*cilium_v2.CiliumNode, error) {
				return lister.Get(key.Name)
			},
		},
	}
}

func CiliumEndpoint(_ K8sInformerFactory, factory CiliumInformerFactory) informerAndLister[*cilium_v2.CiliumEndpoint] {
	informer := factory.Cilium().V2().CiliumEndpoints().Informer()
	lister := factory.Cilium().V2().CiliumEndpoints().Lister()
	return informerAndLister[*cilium_v2.CiliumEndpoint]{
		informer,
		genericLister[*cilium_v2.CiliumEndpoint]{
			List: lister.List,
			Get: func(key K8sKey) (*cilium_v2.CiliumEndpoint, error) {
				return lister.CiliumEndpoints(key.Namespace).Get(key.Name)
			},
		},
	}
}

func NewK8sResourceModule[T k8sRuntime.Object](provider ResourceProvider[T]) fx.Option {
	var obj T
	return fx.Module(
		fmt.Sprintf("k8s-resource[%T]", obj),
		fx.Provide(provider, newK8sResourceHandle[T]),
	)
}

func newK8sResourceHandle[T k8sRuntime.Object](lc fx.Lifecycle, il informerAndLister[T]) (K8sResource[T], error) {
	var empty T

	handle := &k8sResourceHandle[T]{
		empty: empty,
		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), fmt.Sprintf("queue[%T]", empty)),
		informer: il.informer,
		lister: il.lister,
	}
	handle.publish, handle.source = eventsource.NewEventSource[K8sKey]()
	handle.informer.AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				key, err := objToKey(obj)
				if err != nil {
					// Do better error handling
					panic(err)
				}
				// This queue will check if the 'key' is already present.
				// If it is, then the 'key' won't be added because the event
				// handler will always fetch the latest state from the
				// object store cache when the 'key' will be processed.
				handle.queue.Add(key)
			},
			UpdateFunc: func(_, newObj interface{}) {
				key, err := objToKey(newObj)
				if err != nil {
					// Do better error handling
					panic(err)
				}
				handle.queue.Add(key)
			},
			DeleteFunc: func(obj interface{}) {
				key, err := objToKey(obj)
				if err != nil {
					// Do better error handling
					panic(err)
				}
				handle.queue.Add(key)
			},
		})

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go handle.processQueue()
			return nil
		},
		OnStop: func(context.Context) error {
			handle.queue.ShutDownWithDrain()
			return nil
		},
	})
	return handle, nil
}

func (h *k8sResourceHandle[T]) ChangedKeys() eventsource.EventSource[K8sKey] {
	return h.source
}

func (h *k8sResourceHandle[T]) Get(key K8sKey) (T, error) {
	return h.lister.Get(key)
}

func (h *k8sResourceHandle[T]) List(selector labels.Selector) ([]T, error) {
	return h.lister.List(selector)
}

// processQueue receives items for the queue and passes them to consumers that
// subscribed via ChangedKeys().Subscribe. Slow consumers will backpressure
// towards the K8s informer through the queue.
func (h *k8sResourceHandle[T]) processQueue() {
	for {
		key, quit := h.queue.Get()
		if quit {
			return
		}
		h.publish(key.(K8sKey))
		h.queue.Forget(key)
		h.queue.Done(key)
	}
}

