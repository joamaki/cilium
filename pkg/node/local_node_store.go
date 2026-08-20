// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"context"
	"log/slog"
	"slices"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"

	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/metrics"
	"github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/rate"
	"github.com/cilium/cilium/pkg/source"
	"github.com/cilium/cilium/pkg/time"
)

// LocalNodeSynchronizer specifies how to build, and keep synchronized the local
// node object.
type LocalNodeSynchronizer interface {
	InitLocalNode(context.Context, *LocalNodeMutator) error
	SyncLocalNode(context.Context, *LocalNodeStore)
	WaitForNodeInformation(context.Context, *LocalNodeStore) error
}

// NodeGetter describes the behavior of a node store used for retrieving the
// local node.
type NodeGetter interface {
	Get(ctx context.Context) (Node, error)
}

// LocalNodeStoreCell provides the LocalNodeStore instance.
// The LocalNodeStore provides a reactive API for observing and updating the
// local node table object.
var LocalNodeStoreCell = cell.Module(
	"local-node-store",
	"Provides LocalNodeStore for observing and updating local node info",

	cell.ProvidePrivate(NewNodeTable),
	metrics.Metric(NewNodeMetrics),
	cell.Provide(NewNodeWriter),
	cell.Provide(NewNodeTableAndLocalNodeStore),
	cell.Provide(NewClusterSizeDependantInterval),
	cell.Invoke(registerNodeMetrics),
	cell.Invoke(registerNodeBackgroundSync),
)

const (
	LocalNodeTableInitializerName = "local"
)

// LocalNodeStoreParams are the inputs needed for constructing LocalNodeStore.
type LocalNodeStoreParams struct {
	cell.In

	Logger      *slog.Logger
	Lifecycle   cell.Lifecycle
	Sync        LocalNodeSynchronizer
	DB          *statedb.DB
	Jobs        job.Group
	ClusterInfo cmtypes.ClusterInfo
	Nodes       statedb.RWTable[*Node]
}

// LocalNodeStore is the canonical owner for the local node object and provides
// a reactive API for observing and updating the state.
type LocalNodeStore struct {
	db    *statedb.DB
	nodes statedb.RWTable[*Node]
	sync  LocalNodeSynchronizer
}

// NodeStore is retained for call sites that predate the LocalNodeStore name.
type NodeStore = LocalNodeStore

// NewNodeTableAndLocalNodeStore constructs [LocalNodeStore] and the node table.
// Ensures that the local node object is present in the table.
func NewNodeTableAndLocalNodeStore(params LocalNodeStoreParams) (
	*LocalNodeStore, NodeGetter, statedb.Table[*Node], error,
) {
	nodeTable := params.Nodes
	wtxn := params.DB.WriteTxn(nodeTable)

	// Register an initializer that'll mark the table initialized once we're done
	// with [LocalNodeSynchronizer.InitLocalNode].
	initDone := nodeTable.RegisterInitializer(wtxn, LocalNodeTableInitializerName)

	// Insert the skeleton local node.
	initial := NewLocalData(types.NewKVStoreData(&types.KVStoreNode{
		Name: types.GetName(), Cluster: params.ClusterInfo.Name,
		ClusterID: params.ClusterInfo.ID, Source: source.Unspec,
	}), LocalNodeInfo{})
	nodeTable.Insert(wtxn, New(initial))
	wtxn.Commit()

	s := &LocalNodeStore{params.DB, nodeTable, params.Sync}

	params.Lifecycle.Append(cell.Hook{
		OnStart: func(ctx cell.HookContext) error {
			wtxn := params.DB.WriteTxn(nodeTable)
			n, _, _ := nodeTable.Get(wtxn, LocalNodeQuery)
			// Delete the initial one as name might change.
			nodeTable.Delete(wtxn, n)

			mutator := newLocalNodeMutator(n.Data)
			err := params.Sync.InitLocalNode(ctx, mutator)
			n = New(mutator.data)
			nodeTable.Insert(wtxn, n)
			initDone(wtxn)
			wtxn.Commit()

			if err != nil {
				return err
			}

			// Start the synchronization process in background
			params.Jobs.Add(
				job.OneShot(
					"sync-local-node",
					func(ctx context.Context, _ cell.Health) error {
						params.Sync.SyncLocalNode(ctx, s)
						return nil
					},
				))
			return nil
		},
	})

	return s, s, nodeTable, nil
}

// WaitForLocalNodeInit waits until the local-node initializer has completed.
// The nodes table may have other pending initializers, so callers interested
// only in the local node need not wait for the whole table to initialize.
func WaitForLocalNodeInit(ctx context.Context, db *statedb.DB, nodes statedb.Table[*Node]) (statedb.ReadTxn, error) {
	const localNodeInitPollInterval = 50 * time.Millisecond

	txn := db.ReadTxn()
	if !slices.Contains(nodes.PendingInitializers(txn), LocalNodeTableInitializerName) {
		return txn, nil
	}

	ticker := time.NewTicker(localNodeInitPollInterval)
	defer ticker.Stop()
	for {
		_, _, watch, _ := nodes.GetWatch(txn, LocalNodeQuery)
		select {
		case <-watch:
		case <-ticker.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		txn = db.ReadTxn()
		if !slices.Contains(nodes.PendingInitializers(txn), LocalNodeTableInitializerName) {
			return txn, nil
		}
	}
}

// observeRatePerSecond sets the maximum number of local node updates per second
// that [LocalNodeStore.Observe] emits. This avoids unnecessary computation when
// there are many rapid changes to the local node.
const observeRatePerSecond = 5

// Observe changes to the local node state.
func (s *LocalNodeStore) Observe(ctx context.Context, next func(Node), complete func(error)) {
	go func() {
		// Wait until the local node is initialized before starting to observe.
		if _, err := WaitForLocalNodeInit(ctx, s.db, s.nodes); err != nil {
			complete(err)
			return
		}

		limiter := rate.NewLimiter(time.Second/observeRatePerSecond, 1)
		defer limiter.Stop()

		defer complete(nil)
		for {
			lns, _, watch, _ := s.nodes.GetWatch(s.db.ReadTxn(), LocalNodeQuery)
			if lns != nil {
				next(*lns)
			}
			if err := limiter.Wait(ctx); err != nil {
				return
			}
			select {
			case <-watch:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Get retrieves the current local node. Use Get() only for inspecting the state,
// e.g. in API handlers. Do not assume the value does not change over time.
// Blocks until the store has been initialized.
func (s *LocalNodeStore) Get(ctx context.Context) (Node, error) {
	txn, err := WaitForLocalNodeInit(ctx, s.db, s.nodes)
	if err != nil {
		return Node{}, err
	}

	ln, _, found := s.nodes.Get(txn, LocalNodeQuery)
	if !found {
		panic("BUG: No local node exists")
	}

	return *ln, nil
}

// Update atomically applies changes made through a transaction-scoped local
// node mutator.
func (s *LocalNodeStore) Update(update func(*LocalNodeMutator)) {
	s.updateData(func(mutator *LocalNodeMutator) { update(mutator) })
}

// UpdateLocalInfo modifies a copy of the local-only value while sharing the
// immutable node address and metadata collections.
func (s *LocalNodeStore) UpdateLocalInfo(update func(*LocalNodeInfo)) {
	s.updateData(func(mutator *LocalNodeMutator) { mutator.UpdateLocalInfo(update) })
}

func (s *LocalNodeStore) updateData(update func(*LocalNodeMutator)) {
	txn := s.db.WriteTxn(s.nodes)
	defer txn.Abort()
	ln, _, found := s.nodes.Get(txn, LocalNodeQuery)
	if !found {
		panic("BUG: No local node exists")
	}
	orig := ln
	mutator := newLocalNodeMutator(ln.Data)
	update(mutator)
	updated := mutator.data
	if EqualData(updated, orig.Data) {
		// No changes.
		return
	}
	ln = New(updated)
	ln.Statuses = orig.Statuses.Pending()

	if orig.Fullname() != ln.Fullname() {
		// Name or cluster has changed, delete first to remove it from the name index.
		s.nodes.Delete(txn, orig)
	}

	s.nodes.Insert(txn, ln)
	txn.Commit()
}

func (s *LocalNodeStore) WaitForNodeInformation(ctx context.Context) error {
	return s.sync.WaitForNodeInformation(ctx, s)
}

func NewTestLocalNodeStore(mockNode Node) *LocalNodeStore {
	db := statedb.New()
	tbl, err := NewNodeTable(db)
	if err != nil {
		panic(err)
	}
	if mockNode.Data == nil {
		mockNode = *New(NewLocalData(types.NewKVStoreData(&types.KVStoreNode{
			Name: types.GetName(),
		}), LocalNodeInfo{}))
	} else if _, local := mockNode.Local(); !local {
		mockNode = *New(NewLocalData(mockNode.Data, LocalNodeInfo{}))
	}
	txn := db.WriteTxn(tbl)
	tbl.Insert(txn, &mockNode)
	txn.Commit()
	return &LocalNodeStore{db, tbl, nil}
}

// LocalNodeStoreTestCell is a convenience for tests that provides a no-op
// [LocalNodeSynchronizer]. Use [LocalNodeStoreCell] in tests when you want
// to provide your own [LocalNodeSynchronizer].
var LocalNodeStoreTestCell = cell.Group(
	cell.Provide(NewNopLocalNodeSynchronizer),
	LocalNodeStoreCell,
)

type nopLocalNodeSynchronizer struct{}

// InitLocalNode implements LocalNodeSynchronizer.
func (n nopLocalNodeSynchronizer) InitLocalNode(context.Context, *LocalNodeMutator) error {
	return nil
}

// SyncLocalNode implements LocalNodeSynchronizer.
func (n nopLocalNodeSynchronizer) SyncLocalNode(context.Context, *LocalNodeStore) {
}

// WaitForNodeInformation implements [LocalNodeSynchronizer].
func (n nopLocalNodeSynchronizer) WaitForNodeInformation(context.Context, *LocalNodeStore) error {
	return nil
}

var _ LocalNodeSynchronizer = nopLocalNodeSynchronizer{}

func NewNopLocalNodeSynchronizer() LocalNodeSynchronizer {
	return nopLocalNodeSynchronizer{}
}
