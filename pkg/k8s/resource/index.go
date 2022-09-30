package resource

import (
	"context"
	"fmt"
	"sync"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/lock"
	"golang.org/x/exp/maps"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
)

type IndexedStore[IxKey comparable, Obj k8sRuntime.Object] interface {
	Get(IxKey) ([]Obj, error)
}

type indexer[IxKey comparable, Obj k8sRuntime.Object] struct {
	// TODO: should this ctx+cancel+wg + start() + stop() pattern
	// be abstracted or not?
	ctx    context.Context
	cancel context.CancelFunc
	mu     lock.RWMutex
	wg     sync.WaitGroup

	res     Resource[Obj]
	toIxKey func(Obj) IxKey
	index   map[IxKey]Set[Key]
}

func (ix *indexer[IxKey, Obj]) processEvent(ev Event[Obj]) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	defer ev.Done(nil)

	fmt.Printf("%T got event %T\n", ix, ev)

	switch ev := ev.(type) {
	case *SyncEvent[Obj]:
		fmt.Printf("%T synced!\n", ix)

	case *UpdateEvent[Obj]:
		key := ix.toIxKey(ev.Object)
		fmt.Printf(">>> Indexed %s under %v\n", ev.Key, key)
		keys := ix.index[key]
		if keys == nil {
			keys = NewSet[Key]()
			ix.index[key] = keys
		}
		keys.Insert(ev.Key)

	case *DeleteEvent[Obj]:
		key := ix.toIxKey(ev.Object)
		s := ix.index[key]
		s.Remove(ev.Key)
	}
}

func (ix *indexer[IxKey, Obj]) start(ctx context.Context) (err error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	fmt.Printf(">>> starting indexer %T\n", ix)

	ix.wg.Add(1)
	ix.res.Observe(
		ix.ctx,
		ix.processEvent,
		func(err error) { ix.wg.Done() })

	fmt.Printf(">>> started %T\n", ix)

	return
}

func (ix *indexer[IxKey, Obj]) stop(context.Context) error {
	ix.cancel()
	ix.wg.Wait()
	return nil
}

func (ix *indexer[IxKey, Obj]) Get(key IxKey) ([]Obj, error) {
	store, err := ix.res.Store(ix.ctx)
	if err != nil {
		return nil, err
	}

	ix.mu.RLock()
	defer ix.mu.RUnlock()

	keys := ix.index[key].Items()

	objs := make([]Obj, 0, len(keys))
	for _, key := range keys {
		obj, exists, err := store.GetByKey(key)
		if err != nil {
			return nil, err
		}
		if exists {
			objs = append(objs, obj)
		}
	}
	return objs, nil
}

func NewIndexedStoreCell[IxKey comparable, Obj k8sRuntime.Object](name string, toKey func(Obj) IxKey) cell.Cell {
	return cell.Provide(func(lc hive.Lifecycle, res Resource[Obj]) IndexedStore[IxKey, Obj] {
		ctx, cancel := context.WithCancel(context.Background())
		ix := &indexer[IxKey, Obj]{
			ctx:     ctx,
			cancel:  cancel,
			res:     res,
			toIxKey: toKey,
			index:   make(map[IxKey]Set[Key]),
		}
		lc.Append(hive.Hook{OnStart: ix.start, OnStop: ix.stop})
		return ix

	})
}

type Set[T comparable] map[T]struct{}

func NewSet[T comparable]() Set[T] {
	return make(map[T]struct{})
}

func (s Set[T]) Items() []T {
	return maps.Keys(s)
}

func (s Set[T]) Contains(x T) bool {
	_, ok := s[x]
	return ok
}

func (s Set[T]) Insert(x T) {
	s[x] = struct{}{}
}

func (s Set[T]) Remove(x T) {
	delete(s, x)
}
