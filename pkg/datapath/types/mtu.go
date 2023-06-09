package types

import "github.com/cilium/cilium/pkg/stream"

type MTU interface {
	// Observable stream of MTU changes
	stream.Observable[int]

	// Get returns the currently chosen MTU
	Get() int
}
