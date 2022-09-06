// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package resources

import (
	"context"
	"errors"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/fields"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	"github.com/cilium/cilium/pkg/stream"
)

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
	isEvent(T)

	// Dispatch dispatches to the right event handler. Prefer this over
	// type switch on event.
	// On errors from update or delete handlers, the event is requeued for
	// processing.
	Dispatch(
		onSync func(Store[T]),
		onUpdate func(Key, T) error,
		onDelete func(Key) error,
	)
}

// SyncEvent is emitted when the store has completed the initial synchronization
// with the cluster.
type SyncEvent[T k8sRuntime.Object] struct {
	Store Store[T]
}

var _ Event[*corev1.Node] = &SyncEvent[*corev1.Node]{}

func (*SyncEvent[T]) isEvent(T) {}
func (s *SyncEvent[T]) Dispatch(onSync func(store Store[T]), onUpdate func(Key, T) error, onDelete func(Key) error) {
	onSync(s.Store)
}

// UpdateEvent is emitted when an object has been added or updated
type UpdateEvent[T k8sRuntime.Object] struct {
	Key     Key
	Object  T
	requeue func()
}

// Requeue inserts the event key back to the queue to be retried later. This is per-subscriber.
func (u *UpdateEvent[T]) Requeue() {
	u.requeue()
}

var _ Event[*corev1.Node] = &UpdateEvent[*corev1.Node]{}

func (*UpdateEvent[T]) isEvent(T) {}
func (ev *UpdateEvent[T]) Dispatch(onSync func(Store[T]), onUpdate func(Key, T) error, onDelete func(Key) error) {
	if err := onUpdate(ev.Key, ev.Object); err != nil {
		ev.requeue()
	}
}

// DeleteEvent is emitted when an object has been deleted
type DeleteEvent[T k8sRuntime.Object] struct {
	Key     Key
	requeue func()
}

func (d *DeleteEvent[T]) Requeue() {
	d.requeue()
}

var _ Event[*corev1.Node] = &DeleteEvent[*corev1.Node]{}

func (*DeleteEvent[T]) isEvent(T) {}
func (ev *DeleteEvent[T]) Dispatch(onSync func(Store[T]), onUpdate func(Key, T) error, onDelete func(Key) error) {
	if err := onDelete(ev.Key); err != nil {
		ev.requeue()
	}
}

// Store is a read-only typed wrapper for cache.Store.
type Store[T k8sRuntime.Object] interface {
	List() []T
	ListKeys() []string
	Get(obj T) (item T, exists bool, err error)
	GetByKey(key string) (item T, exists bool, err error)

	CacheStore() cache.Store
}

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

func (s *typedStore[T]) GetByKey(key string) (item T, exists bool, err error) {
	var itemAny any
	itemAny, exists, err = s.store.GetByKey(key)
	item = itemAny.(T)
	return
}

func (s *typedStore[T]) CacheStore() cache.Store {
	return s.store
}

// NewResource creates a stream of events from a ListerWatcher.
// The initial set of objects is emitted first as UpdateEvents, followed by a SyncEvent, after
// which updates follow. SyncEvent contains a read-only handle onto the underlying store.
//
// Returns an observable for the stream of events and the function to run the resource.
func NewResource[T k8sRuntime.Object](rootCtx context.Context, lw cache.ListerWatcher) (src stream.Observable[Event[T]], run func()) {
	var (
		mu     sync.RWMutex
		subId  int
		queues = make(map[int]workqueue.RateLimitingInterface)
		obj    T
	)

	// Helper to push the key to all subscribed queues.
	push := func(key Key) {
		mu.RLock()
		for _, queue := range queues {
			queue.Add(key)
		}
		mu.RUnlock()
	}

	handlerFuncs :=
		cache.ResourceEventHandlerFuncs{
			AddFunc:    func(obj interface{}) { push(NewKey(obj)) },
			UpdateFunc: func(old interface{}, new interface{}) { push(NewKey(new)) },
			DeleteFunc: func(obj interface{}) { push(NewKey(obj)) },
		}

	store, informer := cache.NewInformer(lw, obj, 0, handlerFuncs)

	run = func() { informer.Run(rootCtx.Done()) }
	src = stream.FuncObservable[Event[T]](
		func(subCtx context.Context, next func(Event[T]), complete func(error)) {
			subCtx, cancel := context.WithCancel(subCtx)

			// Subscribe to changes first so they would not be missed.
			mu.Lock()
			subId++
			queue := workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter())
			queues[subId] = queue
			mu.Unlock()

			// Goroutine to propagate the events
			go func() {
				defer cancel()

				// Wait for cache to be synced before emitting the initial set.
				if !cache.WaitForCacheSync(rootCtx.Done(), informer.HasSynced) {
					mu.Lock()
					delete(queues, subId)
					mu.Unlock()
					complete(errors.New("Resource is shutting down"))
					return
				}

				// Emit the initial set of objects followed by the sync event
				initialVersions := make(map[Key]string)
				for _, obj := range store.List() {
					key := NewKey(obj.(T))
					requeue := func() { queue.Add(key) }
					next(&UpdateEvent[T]{key, obj.(T), requeue})
					if v := resourceVersion(obj); v != "" {
						initialVersions[key] = v
					}
				}
				next(&SyncEvent[T]{&typedStore[T]{store}})

				var completeErr error
				for {
					rawKey, shutdown := queue.Get()
					if shutdown {
						break
					}
					key := rawKey.(Key)

					rawObj, exists, err := store.GetByKey(key.String())
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

					// TODO: Is this enough, or should we make sure not to call Forget if
					// requeued?
					requeued := false
					requeue := func() {
						requeued = true
						queue.AddRateLimited(key)
					}

					obj, ok := rawObj.(T)
					if exists && ok {
						next(&UpdateEvent[T]{key, obj, requeue})
					} else {
						next(&DeleteEvent[T]{key, requeue})
					}

					queue.Done(rawKey)
					if !requeued {
						queue.Forget(rawKey)
					}
				}

				mu.Lock()
				delete(queues, subId)
				mu.Unlock()
				complete(completeErr)
			}()

			// Goroutine to wait for cancellation
			go func() {
				select {
				case <-rootCtx.Done():
				case <-subCtx.Done():
				}
				queue.ShutDownWithDrain()
			}()
		})

	return
}

// NewResourceFromClient creates a stream of events from a k8s REST client for
// the given resource and namespace.
func NewResourceFromClient[T k8sRuntime.Object](
	ctx context.Context,
	resource string,
	namespace string,
	client rest.Interface,
) (src stream.Observable[Event[T]], run func()) {
	lw := cache.NewListWatchFromClient(
		client,
		resource,
		namespace,
		fields.Everything(),
	)
	return NewResource[T](ctx, lw)
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
