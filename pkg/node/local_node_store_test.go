package node

import (
	"context"
	"sync"
	"testing"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

func TestLocalNodeStore(t *testing.T) {
	var waitObserve sync.WaitGroup
	var changes []*LocalNode

	waitObserve.Add(1)

	// observe observes changes to the LocalNodeStore and completes
	// waitObserve after the last change has been observed. We don't guarantee
	// to observe all changes as observing happens asynchronously.
	observe := func(store LocalNodeStore) {
		go func() {
			store.Observe(context.TODO(),
				func(n *LocalNode) error {
					changes = append(changes, n)
					if n.IPSecKeyIdentity == 3 {
						waitObserve.Done()
					}
					return nil
				})
		}()
	}

	// update adds a start hook to the application that modifies
	// the local node.
	update := func(lc fx.Lifecycle, store LocalNodeStore) {
		lc.Append(fx.Hook{
			OnStart: func(context.Context) error {
				store.Update(func(n *LocalNode) {
					n.IPSecKeyIdentity = 1
				})
				store.Update(func(n *LocalNode) {
					n.IPSecKeyIdentity = 2
				})
				store.Update(func(n *LocalNode) {
					n.IPSecKeyIdentity = 3
				})
				return nil
			},
		})
	}

	// TODO: Add some "NewForTest" helper to hive so that we don't
	// need to have tests depend on viper and pflag.
	hive := hive.New(
		viper.New(),
		pflag.NewFlagSet("", pflag.ContinueOnError),

		LocalNodeStoreCell,

		hive.NewCell("test",
			fx.Invoke(observe),
			fx.Invoke(update)),
	)

	app, err := hive.TestApp(t)
	if err != nil {
		t.Fatal(err)
	}

	app.RequireStart()

	// Wait for observe() to observe at least one change.
	waitObserve.Wait()

	app.RequireStop()

	if len(changes) < 1 || len(changes) > 3 {
		t.Fatalf("observed unexpected number of changes, expected 1, 2 or 3, got: %d", len(changes))
	}

	lastChange := changes[len(changes)-1]
	if lastChange.IPSecKeyIdentity != 3 {
		t.Fatalf("expected to observe last change with IPSecKeyIdentity=3, got %d", lastChange.IPSecKeyIdentity)
	}
}
