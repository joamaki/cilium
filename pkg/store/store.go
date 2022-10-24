// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package store

import (
	"context"
)

// Store is an abstraction for an observable key-value store.
type Store[Key comparable, Value any] interface {
	Observe(ctx context.Context, next func(Event[Key, Value]), complete func(error))

	Get(Key) (Value, error)
	IterKeys() Iter[Key]
	IterValues() Iter[Value]

	Upsert(Key, Value) error
	Delete(Key) error

	Tx() TransactionBuilder[Key, Value]
}

// Merge merges transactions into one large transaction. Transactions
// can be from different stores.
func Merge(txs ...Transaction) Transaction {
	panic("TBD")
}

type Transaction interface {
	Commit() error
}

type TransactionBuilder[Key, Value any] interface {
	Abort()
	Build() Transaction
}

type Iter[T any] interface {
	Next() (T, bool)
}

type Event[Key comparable, Value any] interface {
	// Process the event by calling the appropriate handler.
	// If the handler returns an error the key is requeued for later reprocessing.
	Process(
		onSync func() error,
		onAdd func(Key, Value) error,
		onUpdate func(Key, Value) error,
		onDelete func(Key, Value) error,
	)
}

type BackingStore[Key comparable, Value any] interface {
	GetByKey(Key) (Value, bool, error)
}

type RealStore[Key comparable, Value any] struct {
	be BackingStore[Key, Value]
}

func (s *RealStore[K,V]) OnChange(key K) {
}

func (s *RealStore[K,V]) OnSync() {
}

/*
type Backend[Key, Value any] interface {
	ListAndWatch(ctx context.Context, onAdd func(Key, Value), onUpdate func(Key, Value), onDelete func(Key)) 
	Upsert(Key, Value) error
	Delete(Key) error
}

type store[Key comparable, Value any] struct {
	mu lock.RWMutex
	backend Backend[Key, Value]
	objects map[Key]Value
}

func New[Key comparable, Value any](backend Backend[Key, Value]) Store[Key, Value] {

}*/
