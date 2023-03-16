package k8s

import (
	"context"

	"github.com/cilium/cilium/pkg/stream"
	"k8s.io/client-go/tools/cache"
)

// TODO generics?
func K8sEvents(lw cache.ListerWatcher) stream.Observable[Event] {
	return stream.FuncObservable[Event](
		func(ctx context.Context, next func(Event), complete func(err error)) {
			store := &storeAdapter{
				onAdd:    func(obj any) { next(Event{TypeAdd, obj}) },
				onUpdate: func(obj any) { next(Event{TypeUpdate, obj}) },
				onDelete: func(obj any) { next(Event{TypeDelete, obj}) },
			}
			reflector := cache.NewReflector(lw, nil, store, 0)
			go func() {
				reflector.Run(ctx.Done())
				complete(nil)
			}()
		})

}

const (
	TypeAdd = iota
	TypeUpdate
	TypeDelete
)

type Event struct {
	Type int
	Obj  any
}

type storeAdapter struct {
	onAdd, onUpdate, onDelete func(any)
}

// Add implements cache.Store
func (s *storeAdapter) Add(obj interface{}) error {
	s.onAdd(obj)
	return nil
}

// Update implements cache.Store
func (s *storeAdapter) Update(obj interface{}) error {
	s.onUpdate(obj)
	return nil
}

// Delete implements cache.Store
func (s *storeAdapter) Delete(obj interface{}) error {
	s.onDelete(obj)
	return nil
}

// Get implements cache.Store
func (*storeAdapter) Get(obj interface{}) (item interface{}, exists bool, err error) {
	panic("unimplemented")
}

// GetByKey implements cache.Store
func (*storeAdapter) GetByKey(key string) (item interface{}, exists bool, err error) {
	panic("unimplemented")
}

// List implements cache.Store
func (*storeAdapter) List() []interface{} {
	panic("unimplemented")
}

// ListKeys implements cache.Store
func (*storeAdapter) ListKeys() []string {
	panic("unimplemented")
}

// Replace implements cache.Store
func (s *storeAdapter) Replace(items []interface{}, resourceVersion string) error {
	for _, item := range items {
		s.onUpdate(item)
	}
	return nil
}

// Resync implements cache.Store
func (*storeAdapter) Resync() error {
	panic("unimplemented")
}

var _ cache.Store = &storeAdapter{}
