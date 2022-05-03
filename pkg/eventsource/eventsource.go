// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package eventsource

import (
	"fmt"
	"runtime"
	"sync"
)

type Event interface {
	// TODO
}

type EventSource[E Event] interface {
	Subscribe(name string, handler func(event E)) UnsubscribeFunc
	SubscribeChan(name string, bufferSize int) (<-chan E, UnsubscribeFunc)
}

type eventSource[E Event] struct {
	sync.RWMutex
	subs  []*subscriber[E]
}

type PublishFunc[E Event] func(ev E) 

func NewEventSource[E Event]() (PublishFunc[E], EventSource[E]) {
	src := &eventSource[E]{}
	return src.publish, src
}

type subscriber[E Event] struct {
	name     string
	from     string
	handler  func(ev E)
}

type UnsubscribeFunc func()

func (src *eventSource[E]) publish(event E) {
	src.RLock()
	for _, sub := range src.subs {
		sub.handler(event)
	}
	src.RUnlock()
}

func (src *eventSource[E]) Subscribe(name string, handler func(event E)) UnsubscribeFunc {
	src.Lock()
	defer src.Unlock()

	from := "<unknown>"
	if pc, _, lineNum, ok := runtime.Caller(1); ok {
		if fn := runtime.FuncForPC(pc); fn != nil {
			from = fmt.Sprintf("%s#%d", fn.Name(), lineNum)
		}
	}

	sub := &subscriber[E]{
		name:     name,
		from:     from,
		handler:  handler,
	}
	src.subs = append(src.subs, sub)
	return func() {
		src.Lock()
		defer src.Unlock()

		for i := range src.subs {
			if src.subs[i] == sub {
				src.subs = deleteUnordered(src.subs, i)
				break
			}
		}
	}
}

func (src *eventSource[E]) SubscribeChan(name string, bufferSize int) (<-chan E, UnsubscribeFunc) {
	src.Lock()
	defer src.Unlock()

	from := "<unknown>"
	if pc, _, lineNum, ok := runtime.Caller(1); ok {
		if fn := runtime.FuncForPC(pc); fn != nil {
			from = fmt.Sprintf("%s#%d", fn.Name(), lineNum)
		}
	}

	events := make(chan E, bufferSize)

	sub := &subscriber[E]{
		name:     name,
		from:     from,
		handler:  func(event E) { events <- event },
	}
	src.subs = append(src.subs, sub)

	unsub := func() {
		src.Lock()
		defer src.Unlock()

		for i := range src.subs {
			if src.subs[i] == sub {
				src.subs = deleteUnordered(src.subs, i)
				break
			}
		}
		close(events)
	}

	return events, unsub
}

func deleteUnordered[T any](slice []T, index int) []T {
	var empty T
	slice[index] = slice[len(slice)-1]
	slice[len(slice)-1] = empty
	return slice[:len(slice)-1]
}

