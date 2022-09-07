// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build !privileged_tests

package promise

import "testing"

func TestPromise(t *testing.T) {
	resolve, promiseI := New[int]()

	promiseU := Map(promiseI, func(n int) uint64 { return uint64(n) * 2 })

	go func() {
		resolve(123)
		resolve(256)
	}()

	if v := promiseI.Await(); v != 123 {
		t.Fatalf("expected 123, got %d", v)
	}
	if v := promiseU.Await(); v != 2*123 {
		t.Fatalf("expected 2*123, got %d", v)
	}
}
