// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	operatorOption "github.com/cilium/cilium/operator/option"
	operatorWatchers "github.com/cilium/cilium/operator/watchers"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/ipam/allocator"
	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	k8sResources "github.com/cilium/cilium/pkg/k8s/resources"
	"github.com/cilium/cilium/pkg/kvstore/store"
	nodeStore "github.com/cilium/cilium/pkg/node/store"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/stream"
	"go.uber.org/fx"
)

var ciliumNodeSyncCell = hive.NewCell(
	"cilium-node-sync",
	fx.Provide(newCiliumNodeSync),
	fx.Invoke(func(*ciliumNodeSync) {}),
)

type ciliumNodeSyncParams struct {
	fx.In

	Lifecycle   OperatorLifecycle
	Clientset   k8sClient.Clientset
	CiliumNodes stream.Observable[k8sResources.Event[*cilium_v2.CiliumNode]]
	NodeManager allocator.NodeEventHandler `optional:"true"`
}

func newCiliumNodeSync(p ciliumNodeSyncParams) *ciliumNodeSync {
	fmt.Printf(">>> newCiliumNodeSync\n")

	s := &ciliumNodeSync{
		params:      p,
		ciliumNodes: make(map[k8sResources.Key]*cilium_v2.CiliumNode),
	}
	p.Lifecycle.AppendOnStart(s.onStartLeading)
	return s
}

type ciliumNodeSync struct {
	params ciliumNodeSyncParams

	ctx               context.Context
	ciliumNodeKVStore *store.SharedStore
	ciliumNodes       map[k8sResources.Key]*cilium_v2.CiliumNode
}

func (s *ciliumNodeSync) onStartLeading(ctx context.Context, withKVStore bool) error {
	fmt.Printf(">>> ciliumNodeSync: on start leading\n")

	s.ctx = ctx

	go func() {
		var err error

		if withKVStore {
			s.ciliumNodeKVStore, err = store.JoinSharedStore(store.Configuration{
				Prefix:     nodeStore.NodeStorePrefix,
				KeyCreator: nodeStore.KeyCreator,
			})
			if err != nil {
				log.WithError(err).Fatal("Unable to connect to kvstore")
			}
		}

		fmt.Printf(">>>  Starting to observe CiliumNodes\n")
		s.params.CiliumNodes.Observe(
			s.ctx,
			func(ev k8sResources.Event[*cilium_v2.CiliumNode]) {
				ev.Dispatch(s.onCiliumNodesSynced, s.onCiliumNodeUpdated, s.onCiliumNodeDeleted)
			},
			func(err error) {
				if err != nil {
					log.Fatal(err)
				}
			},
		)
	}()

	return nil
}

func (s *ciliumNodeSync) onCiliumNodesSynced(store k8sResources.Store[*cilium_v2.CiliumNode]) {
	fmt.Printf(">>> onCiliumNodesSynced\n")

	if s.ciliumNodeKVStore != nil {
		// Since we processed all events received from k8s we know that
		// at this point the list in 'store' should be the source of
		// truth and we need to delete all nodes in the kvNodeStore that are
		// *not* present in the ciliumNodeStore.
		listOfCiliumNodes := store.ListKeys()

		kvStoreNodes := s.ciliumNodeKVStore.SharedKeysMap()

		for _, ciliumNode := range listOfCiliumNodes {
			// The remaining kvStoreNodes are leftovers that need to be GCed
			kvStoreNodeName := nodeTypes.GetKeyNodeName(option.Config.ClusterName, ciliumNode)
			delete(kvStoreNodes, kvStoreNodeName)
		}

		if len(listOfCiliumNodes) == 0 && len(kvStoreNodes) != 0 {
			log.Warn("Preventing GC of nodes in the KVStore due the nonexistence of any CiliumNodes in kube-apiserver")
			return
		}

		for _, kvStoreNode := range kvStoreNodes {
			// Only delete the nodes that belong to our cluster
			if strings.HasPrefix(kvStoreNode.GetKeyName(), option.Config.ClusterName) {
				s.ciliumNodeKVStore.DeleteLocalKey(s.ctx, kvStoreNode)
			}
		}
	}

	ciliumNodeStore = store.CacheStore()
	close(k8sCiliumNodesCacheSynced)

	operatorWatchers.RunCiliumNodeGC(s.ctx, s.params.Clientset, store.CacheStore(), operatorOption.Config.NodesGCInterval)
}

func (s *ciliumNodeSync) onCiliumNodeUpdated(key k8sResources.Key, node *cilium_v2.CiliumNode) error {
	fmt.Printf(">>> onCiliumNodeUpdated...\n")

	s.ciliumNodes[key] = node
	if s.params.NodeManager != nil && !s.params.NodeManager.Update(node) {
		return errors.New("NodeManager.Update failed")
	}

	if s.ciliumNodeKVStore != nil {
		nodeNew := nodeTypes.ParseCiliumNode(node)
		if err := s.ciliumNodeKVStore.UpdateKeySync(s.ctx, &nodeNew); err != nil {
			return err
		}
	}
	return nil
}

func (s *ciliumNodeSync) onCiliumNodeDeleted(key k8sResources.Key) error {
	node := s.ciliumNodes[key]

	if s.params.NodeManager != nil {
		s.params.NodeManager.Delete(node)
	}

	if s.ciliumNodeKVStore != nil {
		nodeDel := ciliumNodeName{
			cluster: option.Config.ClusterName,
			name:    node.Name,
		}
		s.ciliumNodeKVStore.DeleteLocalKey(s.ctx, &nodeDel)
	}

	delete(s.ciliumNodes, key)

	return nil
}
