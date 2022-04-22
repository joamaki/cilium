package k8s

import (
	"context"
	"fmt"

	"github.com/cilium/cilium/pkg/k8s/informer"
	v1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"go.uber.org/fx"
	"k8s.io/apimachinery/pkg/fields"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/cilium/cilium/pkg/eventsource"
)

type AddEvent[T k8sRuntime.Object] struct {
	NewObject *T
}

type UpdateEvent[T k8sRuntime.Object] struct {
	OldObject *T
	NewObject *T
}

type DeleteEvent[T k8sRuntime.Object] struct {
	DeletedObject *T
}

type Sources[T k8sRuntime.Object] struct {
	fx.Out

	Added eventsource.EventSource[AddEvent[T]]
	Updated eventsource.EventSource[UpdateEvent[T]]
	Deleted eventsource.EventSource[DeleteEvent[T]]
}

type GenWatcherParams struct {
	Resource string
	Name string
}

type GenStore[T k8sRuntime.Object] struct {
	cache.Store
}

func NewGenWatcherModule[T k8sRuntime.Object](resource string, name string) fx.Option {
	return fx.Module(
		fmt.Sprintf("watcher-%s", resource),
		fx.Supply(GenWatcherParams{resource, name}),
		fx.Provide(newGenWatcher[T]),
	)
}


func newGenWatcher[T k8sRuntime.Object](lc fx.Lifecycle, params GenWatcherParams, k8sClient *K8sClient) (GenStore[T], *Sources[T]) {
	var proto T // TODO: does this work if T is e.g. *v1.Node?
	var sources Sources[T]
	var added eventsource.PublishFunc[AddEvent[T]]
	var updated eventsource.PublishFunc[UpdateEvent[T]]
	var deleted eventsource.PublishFunc[DeleteEvent[T]]

	added, sources.Added = eventsource.NewEventSource[AddEvent[T]]()
	updated, sources.Updated = eventsource.NewEventSource[UpdateEvent[T]]()
	deleted, sources.Deleted = eventsource.NewEventSource[DeleteEvent[T]]()

	store, controller := informer.NewInformer(
		cache.NewListWatchFromClient(k8sClient.CoreV1().RESTClient(),
			params.Resource, v1.NamespaceAll,
			fields.ParseSelectorOrDie("metadata.name="+params.Name)),
		proto,
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				typedObj, ok := obj.(*T)
				// TODO "DeletedFinalStateUnknown"
				if ok {
					added(AddEvent[T]{typedObj})
				}
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				typedOldObj, okOld := oldObj.(*T)
				typedNewObj, okNew := newObj.(*T)
				// TODO equality?
				// TODO "DeletedFinalStateUnknown"
				if okOld && okNew {
					updated(UpdateEvent[T]{typedOldObj, typedNewObj})
				}
			},
			DeleteFunc: func(obj interface{}) {
				typedObj, ok := obj.(*T)
				// TODO "DeletedFinalStateUnknown"
				if ok {
					deleted(DeleteEvent[T]{typedObj})
				}
			},
		},
		nil,
	)

	// TODO cache sync?

	stop := make(chan struct{})

	lc.Append(
		fx.Hook{
			OnStart: func(context.Context) error {
				go controller.Run(stop)
				return nil
			},
			OnStop: func(context.Context) error {
				close(stop)
				return nil
			},
		})

	return GenStore[T]{store}, &sources
}
