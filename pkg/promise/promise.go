// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package promise

import (
	"sync"

	"github.com/cilium/cilium/pkg/lock"
)

// A promise for a future value.
type Promise[T any] interface {
	// Await blocks until the value is resolved.
	Await() T
}

// New creates a new promise for value T.
// Returns function to resolve the promise, and the promise itself.
func New[T any]() (func(T), Promise[T]) {
	p := &promise[T]{}
	p.cond = sync.NewCond(p)
	return p.resolve, p
}

// Map transforms the value of a promise with the provided function.
func Map[A, B any](p Promise[A], transform func(A) B) Promise[B] {
	resolve, p2 := New[B]()
	go func() {
		resolve(transform(p.Await()))
	}()
	return p2
}

type promise[T any] struct {
	lock.Mutex
	cond *sync.Cond
	resolved bool
	value T
}

func (p *promise[T]) resolve(value T) {
	p.Lock()
	defer p.Unlock()

	if p.resolved {
		return
	}
	p.resolved = true
	p.value = value
	p.cond.Broadcast()
}

func (p *promise[T]) Await() T {
	p.Lock()
	defer p.Unlock()
	for !p.resolved {
		p.cond.Wait()
	}
	return p.value
}
