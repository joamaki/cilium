package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/cilium/cilium/pkg/stream"
	"github.com/cockroachdb/pebble"
	"github.com/gogo/protobuf/proto"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

type Iter[T any] interface {
	Next() (T, bool)
	Stop()
}

func iterToSlice[A, B any](it Iter[A], conv func(A) B) []B {
	items := []B{}
	for {
		v, ok := it.Next()
		if !ok {
			break
		}
		items = append(items, conv(v))
	}
	return items
}

type mappedIter[A, B any] struct {
	fn   func(A) B
	orig Iter[A]
}

func (m mappedIter[A, B]) Next() (b B, ok bool) {
	a, ok := m.orig.Next()
	if !ok {
		return b, ok
	}
	return m.fn(a), true
}

func (m mappedIter[A, B]) Stop() {
	m.orig.Stop()
}

type StoreReader[Key, Value any] interface {
	stream.Observable[StoreEvent[Key, Value]]
	Get(Key) (Value, bool, error)
	Keys() Iter[Key]
	Values() Iter[Value]
}

type StoreWriter[Key, Value any] interface {
	Create(Key, Value) error
	Update(Key, Value) error
	Delete(Key) error
}

const (
	EvCreated = iota
	EvUpdated
	EvDeleted
)

type StoreEvent[Key, Value any] struct {
	Kind int
	Key  Key
}

type KeyValueStore[Key any, Value any] interface {
	StoreReader[Key, Value]
	StoreWriter[Key, Value]
	Close() error
}

type cacheStoreAdapter[Value k8sRuntime.Object] struct {
	store KeyValueStore[string, Value]
}

// Add implements cache.Store
func (a *cacheStoreAdapter[Value]) Add(obj interface{}) error {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return err
	}
	val, ok := obj.(Value)
	if !ok {
		return fmt.Errorf("object %T is not expected type %T", obj, val)
	}
	return a.store.Create(key, val)
}

// Delete implements cache.Store
func (a *cacheStoreAdapter[Value]) Delete(obj interface{}) error {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return err
	}
	return a.store.Delete(key)
}

// Get implements cache.Store
func (a *cacheStoreAdapter[Value]) Get(obj interface{}) (interface{}, bool, error) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return nil, false, err
	}
	return a.store.Get(key)
}

// GetByKey implements cache.Store
func (a *cacheStoreAdapter[Value]) GetByKey(key string) (item interface{}, exists bool, err error) {
	return a.store.Get(key)
}

// List implements cache.Store
func (a *cacheStoreAdapter[Value]) List() []interface{} {
	return iterToSlice(a.store.Values(), func(v Value) any { return v })
}

// ListKeys implements cache.Store
func (a *cacheStoreAdapter[Value]) ListKeys() []string {
	return iterToSlice(a.store.Keys(), func(s string) string { return s })
}

// Replace implements cache.Store
func (*cacheStoreAdapter[Value]) Replace([]interface{}, string) error {
	panic("unimplemented")
}

// Resync implements cache.Store
func (*cacheStoreAdapter[Value]) Resync() error {
	return nil
}

// Update implements cache.Store
func (a *cacheStoreAdapter[Value]) Update(obj interface{}) error {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return err
	}
	val, ok := obj.(Value)
	if !ok {
		return fmt.Errorf("object %T is not expected type %T", obj, val)
	}
	return a.store.Update(key, val)
}

var _ cache.Store = &cacheStoreAdapter[k8sRuntime.Object]{}

// Pebble backed store:

type PebbleKeyValueStore struct {
	stream.Observable[StoreEvent[[]byte, []byte]]
	emit     func(StoreEvent[[]byte, []byte])
	complete func(error)

	db *pebble.DB
}

func NewPebbleKeyValueStore(dbFile string) (*PebbleKeyValueStore, error) {
	db, err := pebble.Open(dbFile, &pebble.Options{})
	if err != nil {
		return nil, err
	}
	p := &PebbleKeyValueStore{db: db}

	// FIXME: Need to stream all current keys on observe
	p.Observable, p.emit, p.complete = stream.Multicast[StoreEvent[[]byte, []byte]]()
	return p, nil
}

func (s *PebbleKeyValueStore) Close() error {
	err := s.db.Close()
	s.complete(err)
	return err
}

func (s *PebbleKeyValueStore) Create(key []byte, value []byte) error {
	err := s.db.Set(key, value, pebble.NoSync)
	if err == nil {
		s.emit(StoreEvent[[]byte, []byte]{Kind: EvCreated, Key: key})
	}
	return err
}

// Delete implements KeyValueStore
func (s *PebbleKeyValueStore) Delete(key []byte) error {
	err := s.db.Delete(key, pebble.Sync)
	if err == nil {
		s.emit(StoreEvent[[]byte, []byte]{Kind: EvDeleted, Key: key})
	}
	return err
}

// Get implements KeyValueStore
func (s *PebbleKeyValueStore) Get(key []byte) (value []byte, exists bool, err error) {
	value, closer, err := s.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return value, false, nil
	} else if err != nil {
		return value, false, err
	}
	if err := closer.Close(); err != nil {
		return value, false, err
	}
	return value, true, err
}

type pebbleIterator struct {
	snapshot *pebble.Snapshot
	iter     *pebble.Iterator
	current  []byte
}

// Next implements Iter
func (it *pebbleIterator) Next() ([]byte, bool) {
	if it.current == nil {
		return nil, false
	}
	this := it.current
	if it.iter.Next() {
		it.current = it.iter.Key()
	} else {
		it.current = nil
		if it.snapshot != nil {
			it.snapshot.Close()
			it.snapshot = nil
		}
	}
	return this, true
}

// Stop implements Iter
func (pebbleIterator) Stop() {
	panic("unimplemented")
}

var _ Iter[[]byte] = &pebbleIterator{}

// Keys implements KeyValueStore
func (s *PebbleKeyValueStore) Keys() Iter[[]byte] {
	snapshot := s.db.NewSnapshot()
	iter := &pebbleIterator{
		snapshot: snapshot,
		iter:     snapshot.NewIter(&pebble.IterOptions{}),
	}
	iter.iter.First()
	return iter
}

// Update implements KeyValueStore
func (s *PebbleKeyValueStore) Update(key []byte, value []byte) error {
	err := s.db.Set(key, value, pebble.NoSync)
	if err == nil {
		s.emit(StoreEvent[[]byte, []byte]{Kind: EvUpdated, Key: key})
	}
	return err
}

// Values implements KeyValueStore
func (*PebbleKeyValueStore) Values() Iter[[]byte] {
	panic("unimplemented")
}

var _ BytesKeyValueStore = &PebbleKeyValueStore{}

type JSONAdapter[Key, Value any] struct {
	stream.Observable[StoreEvent[Key, Value]]
	store BytesKeyValueStore
}

type BytesKeyValueStore = KeyValueStore[[]byte, []byte]

func mapJSONStoreEvent[Key, Value any](ev StoreEvent[[]byte, []byte]) StoreEvent[Key, Value] {
	var key Key
	json.Unmarshal(ev.Key, &key)
	return StoreEvent[Key, Value]{
		Kind: ev.Kind,
		Key:  key,
	}
}

func NewJSONAdapter[Key, Value any](store BytesKeyValueStore) *JSONAdapter[Key, Value] {
	return &JSONAdapter[Key, Value]{
		Observable: stream.Map(store, mapJSONStoreEvent[Key, Value]),
		store:      store,
	}
}

// Get implements KeyValueStore
func (j *JSONAdapter[Key, Value]) Get(key Key) (v Value, exists bool, err error) {
	keyBytes, err := json.Marshal(key)
	if err != nil {
		return v, false, err
	}
	valueBytes, exists, err := j.store.Get(keyBytes)
	if err != nil || !exists {
		return v, false, err
	}
	err = json.Unmarshal(valueBytes, &v)
	return v, true, err
}

// Keys implements KeyValueStore
func (j *JSONAdapter[Key, Value]) Keys() Iter[Key] {
	return mappedIter[[]byte, Key]{
		func(b []byte) Key {
			var k Key
			json.Unmarshal(b, &k)
			return k
		},
		j.store.Keys(),
	}
}

// Values implements KeyValueStore
func (*JSONAdapter[Key, Value]) Values() Iter[Value] {
	panic("unimplemented")
}

// Create implements KeyValueStore
func (j *JSONAdapter[Key, Value]) Create(k Key, v Value) error {
	keyBytes, err := json.Marshal(k)
	if err != nil {
		return err
	}
	valueBytes, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return j.store.Create(keyBytes, valueBytes)
}

// Delete implements KeyValueStore
func (*JSONAdapter[Key, Value]) Delete(Key) error {
	panic("unimplemented")
}

// Update implements KeyValueStore
func (*JSONAdapter[Key, Value]) Update(Key, Value) error {
	panic("unimplemented")
}

// Close implements KeyValueStore
func (*JSONAdapter[Key, Value]) Close() error {
	panic("unimplemented")
}

var _ KeyValueStore[string, string] = &JSONAdapter[string, string]{}

type ProtoMarshalerUnmarshaler interface {
	proto.Marshaler
	proto.Unmarshaler
}

type ProtobufAdapter[Key, Value ProtoMarshalerUnmarshaler] struct {
	store BytesKeyValueStore
}

func NewProtobufAdapter[Key, Value ProtoMarshalerUnmarshaler](store BytesKeyValueStore) *ProtobufAdapter[Key, Value] {
	return &ProtobufAdapter[Key, Value]{store: store}
}

// Get implements KeyValueStore
func (j *ProtobufAdapter[Key, Value]) Get(key Key) (v Value, exists bool, err error) {
	keyBytes, err := key.Marshal()
	if err != nil {
		return v, false, err
	}
	valueBytes, exists, err := j.store.Get(keyBytes)
	if err != nil || !exists {
		return v, false, err
	}
	err = v.Unmarshal(valueBytes)
	return v, true, err
}

// Keys implements KeyValueStore
func (*ProtobufAdapter[Key, Value]) Keys() Iter[Key] {
	panic("unimplemented")
}

// Values implements KeyValueStore
func (*ProtobufAdapter[Key, Value]) Values() Iter[Value] {
	panic("unimplemented")
}

// Create implements KeyValueStore
func (j *ProtobufAdapter[Key, Value]) Create(k Key, v Value) error {
	keyBytes, err := k.Marshal()
	if err != nil {
		return err
	}
	valueBytes, err := v.Marshal()
	if err != nil {
		return err
	}
	return j.store.Create(keyBytes, valueBytes)
}

// Delete implements KeyValueStore
func (*ProtobufAdapter[Key, Value]) Delete(Key) error {
	panic("unimplemented")
}

// Update implements KeyValueStore
func (*ProtobufAdapter[Key, Value]) Update(Key, Value) error {
	panic("unimplemented")
}

// Close implements KeyValueStore
func (*ProtobufAdapter[Key, Value]) Close() error {
	panic("unimplemented")
}

type IndexedStoreReader[Key, Value any, Index comparable] struct {
	Store KeyValueStore[Key, Value]

	mu sync.RWMutex
	ctx context.Context
	cancel context.CancelFunc
	index map[Index]Key
	keyToIndex func(Key) Index
}

func NewIndexedStoreReader[Key, Value any, Index comparable](
	store KeyValueStore[Key, Value],
	keyToIndex func(Key) Index,
) *IndexedStoreReader[Key, Value, Index] {
	ix := &IndexedStoreReader[Key, Value, Index]{
		Store: store,
		keyToIndex: keyToIndex,
	}
	ix.ctx, ix.cancel = context.WithCancel(context.Background())
	store.Observe(ix.ctx, ix.onEvent, func(error){})
	return ix
}

func (ix *IndexedStoreReader[Key, Value, Index]) onEvent(ev StoreEvent[Key, Value]) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	switch ev.Kind {
	case EvCreated, EvUpdated:
		ix.index[ix.keyToIndex(ev.Key)] = ev.Key
	case EvDeleted:
		delete(ix.index, ix.keyToIndex(ev.Key))
	}
}

// Observe implements KeyValueStore
func (ix *IndexedStoreReader[Key, Value, Index]) Observe(ctx context.Context, next func(StoreEvent[Index, Value]), complete func(error)) {
	ix.Store.Observe(ctx,
	                 func(ev StoreEvent[Key, Value]) {
		                 next(StoreEvent[Index, Value]{
			                 Kind: ev.Kind,
			                 Key: ix.keyToIndex(ev.Key),
		                 })
	                 },
	                 complete)
}

// Get implements KeyValueStore
func (ix *IndexedStoreReader[Key, Value, Index]) Get(i Index) (v Value, exists bool, err error) {
	ix.mu.RLock()
	key, ok := ix.index[i]
	ix.mu.RUnlock()

	if !ok {
		return v, false, nil
	}
	return ix.Store.Get(key)
}

// Keys implements KeyValueStore
func (ix *IndexedStoreReader[Key, Value, Index]) Keys() Iter[Index] {
	return mappedIter[Key, Index]{
		ix.keyToIndex,
		ix.Store.Keys(),
	}
}

// Values implements KeyValueStore
func (ix *IndexedStoreReader[Key, Value, Index]) Values() Iter[Value] {
	return ix.Store.Values()
}

// Close implements KeyValueStore
func (ix *IndexedStoreReader[Key, Value, Index]) Close() error {
	ix.cancel()
	return ix.Store.Close()
}

var _ StoreReader[int64, string] = &IndexedStoreReader[string, string, int64]{}
