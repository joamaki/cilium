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
}

type eventSource[E Event] struct {
	sync.RWMutex
	subs  []*subscriber[E]
}

type PublishFunc[E Event] func(ev E) 

func NewEventSource[E Event]() (PublishFunc[E], EventSource[E]) {
	src := &eventSource[E]{}

	publish := func(ev E) {
		src.RLock()
		for _, sub := range src.subs {
			sub.handler(ev)
		}
		src.RUnlock()
	}

	return publish, src
}

type subscriber[E Event] struct {
	name     string
	from     string
	handler  func(ev E)
}

type UnsubscribeFunc func()

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
				deleteUnordered(src.subs, i)
				break
			}
		}
	}
}

func deleteUnordered[T any](slice []T, index int) []T {
	var empty T
	slice[index] = slice[len(slice)-1]
	slice[len(slice)-1] = empty
	return slice[:len(slice)-1]
}

