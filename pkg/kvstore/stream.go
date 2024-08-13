// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package kvstore

import (
	"context"

	"github.com/cilium/stream"
)

type TypedKeyValueEvent[T any] struct {
	Typ   EventType
	Key   string
	Value T
}

// EventStream constructs an observable stream of events for the given prefix.
func EventStream[T any](backend BackendOperations, prefix string, decode func([]byte) (T, bool)) stream.Observable[TypedKeyValueEvent[T]] {
	const chanSize = 16
	return stream.FuncObservable[TypedKeyValueEvent[T]](
		func(ctx context.Context, next func(TypedKeyValueEvent[T]), complete func(error)) {
			go func() {
				watcher := backend.ListAndWatch(ctx, prefix, chanSize)
				for event := range watcher.Events {
					typedEvent := TypedKeyValueEvent[T]{
						Typ: event.Typ,
						Key: event.Key,
					}
					if len(event.Value) > 0 {
						if obj, ok := decode(event.Value); ok {
							typedEvent.Value = obj
						}
					}
					next(typedEvent)
				}
				complete(nil)
			}()
		})
}
