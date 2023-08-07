// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package k8s

import (
	"context"
	"fmt"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/statedb2"
	"github.com/cilium/cilium/pkg/statedb2/index"
	"github.com/cilium/cilium/pkg/stream"
)

type tableReflectorParams[Obj runtime.Object] struct {
	cell.In

	Lifecycle     hive.Lifecycle
	DB            *statedb2.DB
	Table         statedb2.Table[Obj]
	NameIndex     statedb2.Index[Obj, Key]
	ListerWatcher cache.ListerWatcher
}

func NewK8sTableCell[Obj runtime.Object](
	tableName string,
	listerWatcherProvider any, // func(...) cache.ListerWatcher.
	extraIndexers ...statedb2.Indexer[Obj],
) cell.Cell {
	nameIndex := newNameIndex[Obj]()
	return cell.Module(
		"k8s-table-"+tableName,
		fmt.Sprintf("Kubernetes objects table %s", tableName),

		cell.Provide(func() statedb2.Index[Obj, Key] { return nameIndex }),
		statedb2.NewTableCell[Obj](
			tableName,
			nameIndex,
			extraIndexers...,
		),
		cell.ProvidePrivate(listerWatcherProvider),
		cell.Invoke(registerTableReflector[Obj]),
	)
}

type Key struct {
	Namespace string
	Name      string
}

func (k Key) IndexKey() index.Key {
	return index.String(k.Namespace + "/" + k.Name)
}

func NewKey(obj runtime.Object) Key {
	meta, err := meta.Accessor(obj)
	if err != nil {
		return Key{}
	}
	if len(meta.GetNamespace()) > 0 {
		return Key{Name: meta.GetName(), Namespace: meta.GetNamespace()}
	}
	return Key{Name: meta.GetName(), Namespace: ""}
}

func newNameIndex[Obj runtime.Object]() statedb2.Index[Obj, Key] {
	return statedb2.Index[Obj, Key]{
		Name: "name",
		FromObject: func(obj Obj) index.KeySet {
			return index.NewKeySet(NewKey(obj).IndexKey())
		},
		FromKey: func(key Key) index.Key {
			return index.String(key.Namespace + "/" + key.Name)
		},
		Unique: true,
	}
}

func registerTableReflector[Obj runtime.Object](p tableReflectorParams[Obj]) {
	tr := &tableReflector[Obj]{params: p}
	p.Lifecycle.Append(tr)
}

type tableReflector[Obj runtime.Object] struct {
	params tableReflectorParams[Obj]
}

func (tr *tableReflector[Obj]) Start(hive.HookContext) error {
	go tr.synchronize()
	return nil
}

func (tr *tableReflector[Obj]) Stop(hive.HookContext) error {
	return nil
}

func (tr *tableReflector[Obj]) synchronize() {
	type entry struct {
		deleted   bool
		name      string
		namespace string
		obj       Obj
	}
	type buffer map[string]entry
	const bufferSize = 64 // TODO benchmark to figure out appropriate size
	const waitTime = 100 * time.Millisecond
	table := tr.params.Table
	db := tr.params.DB

	src := stream.BufferBy(
		k8sEventObservable(tr.params.ListerWatcher),
		bufferSize,
		waitTime,

		// Buffer the events into a map, coalescing them by key.
		func(buf buffer, ev cacheStoreEvent) buffer {
			if buf == nil {
				buf = make(buffer, bufferSize)
			}
			var entry entry
			if ev.Type == cacheStoreDelete {
				entry.deleted = true
			} else {
				var ok bool
				entry.obj, ok = ev.Obj.(Obj)
				if !ok {
					panic(fmt.Sprintf("%T internal error: Object %T not of correct type", tr, ev.Obj))
				}
			}
			var key string
			if d, ok := ev.Obj.(cache.DeletedFinalStateUnknown); ok {
				key = d.Key
				entry.namespace, entry.name, _ = cache.SplitMetaNamespaceKey(d.Key)
			} else {
				meta, err := meta.Accessor(ev.Obj)
				if err != nil {
					panic(fmt.Sprintf("%T internal error: meta.Accessor failed: %s", tr, err))
				}
				entry.name = meta.GetName()
				if ns := meta.GetNamespace(); ns != "" {
					key = ns + "/" + meta.GetName()
					entry.namespace = ns
				} else {
					key = meta.GetName()
				}
			}
			buf[key] = entry
			return buf
		},

		// Reset by allocating a new buffer.
		func(buffer) buffer {
			return make(buffer, bufferSize)
		},
	)

	commitBuffer := func(buf buffer) {
		txn := db.WriteTxn(table)
		for _, entry := range buf {
			if !entry.deleted {
				if _, _, err := table.Insert(txn, entry.obj); err != nil {
					// TODO bad schema, how do we want to fail?
					panic(err)
				}
			} else {
				obj, _, ok := table.First(txn, tr.params.NameIndex.Query(Key{entry.namespace, entry.name}))
				if ok {
					if _, _, err := table.Delete(txn, obj); err != nil {
						// TODO bad schema, how do we want to fail?
						panic(err)
					}
				}
			}
		}
		txn.Commit()
	}

	src.Observe(
		context.TODO(),
		commitBuffer,
		func(err error) {},
	)

}

var _ hive.HookInterface = (*tableReflector[*v1.Node])(nil)

// k8sEventObservable turns a ListerWatcher into an observable using the client-go's Reflector.
// TODO: catch watch errors and log or update metrics. Emit watch error as event?
func k8sEventObservable(lw cache.ListerWatcher) stream.Observable[cacheStoreEvent] {
	return stream.FuncObservable[cacheStoreEvent](
		func(ctx context.Context, next func(cacheStoreEvent), complete func(err error)) {
			store := &cacheStoreListener{
				onAdd:    func(obj any) { next(cacheStoreEvent{cacheStoreAdd, obj}) },
				onUpdate: func(obj any) { next(cacheStoreEvent{cacheStoreUpdate, obj}) },
				onDelete: func(obj any) { next(cacheStoreEvent{cacheStoreDelete, obj}) },
			}
			reflector := cache.NewReflector(lw, nil, store, 0)
			go func() {
				reflector.Run(ctx.Done())
				complete(nil)
			}()
		})
}

const (
	cacheStoreAdd = iota
	cacheStoreUpdate
	cacheStoreDelete
)

type cacheStoreEvent struct {
	Type int
	Obj  any
}

// cacheStoreListener implements the methods used by the cache reflector and
// calls the given handlers for added, updated and deleted objects.
type cacheStoreListener struct {
	onAdd, onUpdate, onDelete func(any)
}

func (s *cacheStoreListener) Add(obj interface{}) error {
	s.onAdd(obj)
	return nil
}

func (s *cacheStoreListener) Update(obj interface{}) error {
	s.onUpdate(obj)
	return nil
}

func (s *cacheStoreListener) Delete(obj interface{}) error {
	s.onDelete(obj)
	return nil
}

func (s *cacheStoreListener) Replace(items []interface{}, resourceVersion string) error {
	for _, item := range items {
		s.onUpdate(item)
	}
	return nil
}

func (*cacheStoreListener) Get(obj interface{}) (item interface{}, exists bool, err error) {
	panic("unimplemented")
}
func (*cacheStoreListener) GetByKey(key string) (item interface{}, exists bool, err error) {
	panic("unimplemented")
}
func (*cacheStoreListener) List() []interface{} { panic("unimplemented") }
func (*cacheStoreListener) ListKeys() []string  { panic("unimplemented") }
func (*cacheStoreListener) Resync() error       { panic("unimplemented") }

var _ cache.Store = &cacheStoreListener{}
