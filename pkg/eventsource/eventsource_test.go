// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package eventsource

import (
	"sync"
	"testing"
)

type TestEvent struct {
}

func TestEventSource(t *testing.T) {

	pub, src := NewEventSource[*TestEvent]()

	test1Count := 0
	test2Count := 0
	test3Count := 0

	unsub1 := src.Subscribe("test1", func(event *TestEvent) { test1Count++ })
	unsub2 := src.Subscribe("test2", func(event *TestEvent) { test2Count++ })
	ch, unsub3 := src.SubscribeChan("test3", 0)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		for range ch {
			test3Count++
		}
		wg.Done()
	}()

	for i := 0; i < 10; i++ {
		pub(&TestEvent{})
	}
	unsub1()
	for i := 0; i < 10; i++ {
		pub(&TestEvent{})
	}
	unsub2()
	for i := 0; i < 10; i++ {
		pub(&TestEvent{})
	}
	unsub3()
	wg.Wait()
	for i := 0; i < 10; i++ {
		pub(&TestEvent{})
	}

	if test1Count != 10 {
		t.Fatalf("expected test1 to receive 10, got %d", test1Count)
	}
	if test2Count != 20 {
		t.Fatalf("expected test2 to receive 20, got %d", test2Count)
	}
	if test3Count != 30 {
		t.Fatalf("expected test3 to receive 30, got %d", test3Count)
	}
}
