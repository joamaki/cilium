package node

import (
	"context"
	"sync"

	"github.com/joamaki/goreactive/stream"
	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/lock"
)

var LocalNodeStoreCell = hive.NewCell(
	"local-node-store",

	fx.Provide(newLocalNodeStore),
)

// localNodeStore implements the LocalNodeStore using a simple in-memory
// backing. Reflecting the state to persistent stores, e.g. kvstore or k8s
// is left to observers.
type localNodeStore struct {
	mu lock.Mutex
	stream.Observable[*LocalNode]

	current *LocalNode
	updates chan *LocalNode
}

var _ LocalNodeStore = &localNodeStore{}

func newLocalNodeStore(lc fx.Lifecycle) (LocalNodeStore, error) {
	ctx, cancel := context.WithCancel(context.Background())

	s := &localNodeStore{
		current: &LocalNode{},
		updates: make(chan *LocalNode),
	}

	// TODO(JM): Add support to stream.ObservableValue to allow creating
	// an unset observable value so I don't need to repeat the implementation
	// here.

	src, connect := stream.Multicast(
		stream.MulticastParams{BufferSize: 16, EmitLatest: true},
		stream.FromChannel(s.updates))
	s.Observable = src

	var wg sync.WaitGroup
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			wg.Add(1)
			go func() { connect(ctx); wg.Done() }()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			close(s.updates)
			wg.Wait()
			return nil
		},
	})

	return s, nil
}

func (s *localNodeStore) Update(mutator func(*LocalNode)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Copy the current value and then mutate it.
	new := *s.current
	mutator(&new)

	// Pass the value to observers.
	s.current = &new
	s.updates <- s.current
}
