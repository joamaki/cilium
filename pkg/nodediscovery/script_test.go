// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package nodediscovery

import (
	"context"
	"maps"
	"testing"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/hive/script"
	"github.com/cilium/hive/script/scripttest"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cilium/cilium/daemon/cmd/cni"
	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive"
	v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/node"
	nodemanager "github.com/cilium/cilium/pkg/node/manager"
	nodestore "github.com/cilium/cilium/pkg/node/store"
	nodetypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/time"
)

// TestScript runs all the testdata/*.txtar script tests. The tests are
// run in parallel. If you need to update the expected files inside the txtar
// files you can run 'go test . -scripttest.update' to update the files.
func TestScript(t *testing.T) {
	now := time.Now
	time.Now = func() time.Time {
		return time.Date(2000, 1, 1, 10, 30, 0, 0, time.UTC)
	}
	t.Cleanup(func() { time.Now = now })
	t.Setenv("TZ", "")

	log := hivetest.Logger(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	scripttest.Test(t,
		ctx,
		func(t testing.TB, args []string) *script.Engine {
			h := hive.New(
				client.FakeClientCell,
				node.LocalNodeStoreCell,

				cell.Module("nodediscovery-test", "test",
					cell.Provide(newNodeDiscovery),
				),

				cell.Invoke(func(lc cell.Lifecycle, nd *NodeDiscovery) {

					lc.Append(cell.Hook{
						OnStart: func(ctx cell.HookContext) error {
							nd.StartDiscovery()
							return nil
						},
					})

				}),

				cell.Provide(
					func() nodemanager.NodeManager { return fakeNodeManager{} },
					func() nodestore.NodeRegistrar { return nodestore.NodeRegistrar{nil} },
					func(cs client.Clientset) k8sGetters { return fakeK8sGetters{cs} },
					func() cni.CNIConfigManager { return nil },
				),
			)

			flags := pflag.NewFlagSet("", pflag.ContinueOnError)
			h.RegisterFlags(flags)

			t.Cleanup(func() {
				assert.NoError(t, h.Stop(log, context.TODO()))
			})
			cmds, err := h.ScriptCommands(log)
			require.NoError(t, err, "ScriptCommands")
			maps.Insert(cmds, maps.All(script.DefaultCmds()))
			return &script.Engine{
				Cmds: cmds,
			}
		}, []string{}, "testdata/*.txtar")
}

type fakeNodeManager struct{}

func (f fakeNodeManager) ClusterSizeDependantInterval(baseInterval time.Duration) time.Duration {
	return 0
}
func (f fakeNodeManager) Enqueue(*nodetypes.Node)                         {}
func (f fakeNodeManager) GetNodeIdentities() []nodetypes.Identity         { return nil }
func (f fakeNodeManager) GetNodes() map[nodetypes.Identity]nodetypes.Node { return nil }
func (f fakeNodeManager) MeshNodeSync()                                   {}
func (f fakeNodeManager) NodeDeleted(n nodetypes.Node)                    {}
func (f fakeNodeManager) NodeSync()                                       {}
func (f fakeNodeManager) NodeUpdated(n nodetypes.Node)                    {}
func (f fakeNodeManager) SetPrefixClusterMutatorFn(mutator func(*nodetypes.Node) []cmtypes.PrefixClusterOpts) {
}
func (f fakeNodeManager) StartNeighborRefresh(nh types.NodeNeighbors)         {}
func (f fakeNodeManager) StartNodeNeighborLinkUpdater(nh types.NodeNeighbors) {}
func (f fakeNodeManager) Subscribe(types.NodeHandler)                         {}
func (f fakeNodeManager) Unsubscribe(types.NodeHandler)                       {}

var _ nodemanager.NodeManager = fakeNodeManager{}

type fakeK8sGetters struct {
	cs client.Clientset
}

// GetCiliumNode implements k8sGetters.
func (f fakeK8sGetters) GetCiliumNode(ctx context.Context, nodeName string) (*v2.CiliumNode, error) {
	obj, err := f.cs.CiliumV2().CiliumNodes().Get(ctx, nodeName, v1.GetOptions{})
	if obj == nil {
		// Give some time for the retry of GetCiliumNode to see the change.
		time.Sleep(50 * time.Millisecond)
	}
	return obj, err
}

var _ k8sGetters = fakeK8sGetters{}
