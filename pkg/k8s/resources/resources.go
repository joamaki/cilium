// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package resources

import (
	"context"
	"errors"
	"sync"

	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/promise"
	"github.com/cilium/cilium/pkg/stream"
)

// Resource provides access to a Kubernetes resource through either a
// stream of events or a read-only store.
type Resource[T k8sRuntime.Object] interface {
	stream.Observable[Event[T]]

	// Store retrieves the read-only store for the resource. Blocks until
	// the store has been synchronized.
	Store() Store[T]
}

// Key of an K8s object, e.g. name and optional namespace.
type Key struct {
	// Name is the name of the object
	Name string

	// Namespace is the namespace, or empty if object is not namespaced.
	Namespace string
}

func (k Key) String() string {
	if len(k.Namespace) > 0 {
		return k.Namespace + "/" + k.Name
	}
	return k.Name
}

func NewKey(obj any) Key {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		namespace, name, _ := cache.SplitMetaNamespaceKey(d.Key)
		return Key{name, namespace}
	}

	meta, err := meta.Accessor(obj)
	if err != nil {
		return Key{}
	}
	if len(meta.GetNamespace()) > 0 {
		return Key{meta.GetName(), meta.GetNamespace()}
	}
	return Key{meta.GetName(), ""}
}

// Event emitted from resource. One of SyncEvent, UpdateEvent or DeleteEvent.
type Event[T k8sRuntime.Object] interface {
	// Dispatch dispatches the event to the right handler. Prefer this over
	// type switch as it allows extending the set of events and having a
	// compile-time error at use-sites.
	Dispatch(
		onSync func(Store[T]),
		onUpdate func(T),
		onDelete func(Key),
	)
}

// SyncEvent is emitted when the store has completed the initial synchronization
// with the cluster.
type SyncEvent[T k8sRuntime.Object] struct {
	Store Store[T]
}

var _ Event[*corev1.Node] = &SyncEvent[*corev1.Node]{}

func (s *SyncEvent[T]) Dispatch(onSync func(store Store[T]), onUpdate func(T), onDelete func(Key)) {
	onSync(s.Store)
}

// UpdateEvent is emitted when an object has been added or updated
type UpdateEvent[T k8sRuntime.Object] struct {
	Object T
}

var _ Event[*corev1.Node] = &UpdateEvent[*corev1.Node]{}

func (ev *UpdateEvent[T]) Dispatch(onSync func(Store[T]), onUpdate func(T), onDelete func(Key)) {
	onUpdate(ev.Object)
}

// DeleteEvent is emitted when an object has been deleted
type DeleteEvent[T k8sRuntime.Object] struct {
	Key Key
}

var _ Event[*corev1.Node] = &DeleteEvent[*corev1.Node]{}

func (ev *DeleteEvent[T]) Dispatch(onSync func(Store[T]), onUpdate func(T), onDelete func(Key)) {
	onDelete(ev.Key)
}

// Store is a read-only typed wrapper for cache.Store.
type Store[T k8sRuntime.Object] interface {
	// List returns all items currently in the store.
	List() []T
	ListKeys() []string

	// Get returns the latest version by deriving the key from the given object.
	Get(obj T) (item T, exists bool, err error)

	// GetByKey returns the latest version of the object with given key.
	GetByKey(key Key) (item T, exists bool, err error)
}

// typedStore implements Store on top of an untyped cache.Store.
type typedStore[T k8sRuntime.Object] struct {
	store cache.Store
}

var _ Store[*corev1.Node] = &typedStore[*corev1.Node]{}

func (s *typedStore[T]) List() []T {
	items := s.store.List()
	result := make([]T, len(items))
	for i := range items {
		result[i] = items[i].(T)
	}
	return result
}

func (s *typedStore[T]) ListKeys() []string {
	return s.store.ListKeys()
}

func (s *typedStore[T]) Get(obj T) (item T, exists bool, err error) {
	var key string
	key, err = cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return
	}
	var itemAny any
	itemAny, exists, err = s.store.GetByKey(key)
	item = itemAny.(T)
	return
}

func (s *typedStore[T]) GetByKey(key Key) (item T, exists bool, err error) {
	var itemAny any
	itemAny, exists, err = s.store.GetByKey(key.String())
	item = itemAny.(T)
	return
}

func resourceVersion(obj any) (version string) {
	if obj != nil {
		meta, err := meta.Accessor(obj)
		if err == nil {
			return meta.GetResourceVersion()
		}
	}
	return ""
}

type resource[T k8sRuntime.Object] struct {
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	queues map[uint64]workqueue.RateLimitingInterface
	subId  uint64

	storePromise promise.Promise[Store[T]]
	resolveStore func(Store[T])

	lw       cache.ListerWatcher
	informer cache.Controller
	store    cache.Store
}

var _ Resource[*corev1.Node] = &resource[*corev1.Node]{}

func newResource[T k8sRuntime.Object](lw cache.ListerWatcher) *resource[T] {
	r := &resource[T]{
		lw:     lw,
		queues: make(map[uint64]workqueue.RateLimitingInterface),
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.resolveStore, r.storePromise = promise.New[Store[T]]()
	return r
}

func (r *resource[T]) Store() Store[T] {
	return r.storePromise.Await()
}

func (r *resource[T]) pushKey(key Key) {
	r.mu.RLock()
	for _, queue := range r.queues {
		queue.Add(key)
	}
	r.mu.RUnlock()
}

func (r *resource[T]) start(startCtx context.Context) error {
	var objType T
	handlerFuncs :=
		cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { r.pushKey(NewKey(obj)) },
			UpdateFunc: func(old interface{}, new interface{}) { r.pushKey(NewKey(new)) },
			DeleteFunc: func(obj interface{}) { r.pushKey(NewKey(obj)) },
		}

	r.store, r.informer = cache.NewInformer(r.lw, objType, 0, handlerFuncs)

	r.wg.Add(2)
	go func() {
		defer r.wg.Done()
		r.informer.Run(r.ctx.Done())
	}()
	go func() {
		defer r.wg.Done()

		// Wait for cache to be synced before resolving the store
		if !cache.WaitForCacheSync(r.ctx.Done(), r.informer.HasSynced) {
			// FIXME need to resolve/reject promise? otoh, to get here, all the
			// dependees would have been stopped already.
			return
		}
		r.resolveStore(&typedStore[T]{r.store})
	}()

	return nil
}

func (r *resource[T]) stop(stopCtx context.Context) error {
	r.cancel()
	r.wg.Wait()
	return nil
}

func (r *resource[T]) Observe(subCtx context.Context, next func(Event[T]), complete func(error)) {
	subCtx, subCancel := context.WithCancel(subCtx)

	// Subscribe to changes first so they would not be missed.
	r.mu.Lock()
	r.subId++
	queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
	r.queues[r.subId] = queue
	r.mu.Unlock()

	// Goroutine to propagate the events
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer subCancel()

		// Wait for cache to be synced before emitting the initial set.
		if !cache.WaitForCacheSync(subCtx.Done(), r.informer.HasSynced) {
			r.mu.Lock()
			delete(r.queues, r.subId)
			r.mu.Unlock()
			complete(errors.New("Resource is shutting down"))
			return
		}

		// Emit the initial set of objects followed by the sync event
		initialVersions := make(map[Key]string)
		for _, obj := range r.store.List() {
			key := NewKey(obj.(T))
			next(&UpdateEvent[T]{obj.(T)})
			if v := resourceVersion(obj); v != "" {
				initialVersions[key] = v
			}
		}
		next(&SyncEvent[T]{&typedStore[T]{r.store}})

		var completeErr error
		for {
			rawKey, shutdown := queue.Get()
			if shutdown {
				break
			}
			key := rawKey.(Key)

			rawObj, exists, err := r.store.GetByKey(key.String())
			if err != nil {
				completeErr = err
				break
			}

			if initialVersions != nil {
				version := resourceVersion(rawObj)
				if initialVersion, ok := initialVersions[key]; ok {
					// We can now forget the initial version.
					delete(initialVersions, key)
					if initialVersion == version {
						// Already emitted, skip.
						queue.Done(rawKey)
						queue.Forget(rawKey)
						continue
					}
				}
				if len(initialVersions) == 0 {
					initialVersions = nil
				}
			}

			obj, ok := rawObj.(T)
			if exists && ok {
				next(&UpdateEvent[T]{obj})
			} else {
				next(&DeleteEvent[T]{key})
			}

			queue.Forget(rawKey)
			queue.Done(rawKey)
		}

		r.mu.Lock()
		delete(r.queues, r.subId)
		r.mu.Unlock()
		complete(completeErr)
	}()

	// Goroutine to wait for cancellation
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		select {
		case <-r.ctx.Done():
			subCancel()
		case <-subCtx.Done():
		}
		queue.ShutDownWithDrain()
	}()
}

func NewResourceConstructor[T k8sRuntime.Object](lw func(c k8sClient.Clientset) cache.ListerWatcher) func(lc fx.Lifecycle, c k8sClient.Clientset) Resource[T] {
	return func(lc fx.Lifecycle, c k8sClient.Clientset) Resource[T] {
		if !c.IsEnabled() {
			return nil
		}

		res := newResource[T](lw(c))
		lc.Append(fx.Hook{OnStart: res.start, OnStop: res.stop})
		return res
	}
}
