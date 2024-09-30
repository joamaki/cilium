// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package ciliumenvoyconfig

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/statedb"
	statedbtest "github.com/cilium/statedb/testutils"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	daemonk8s "github.com/cilium/cilium/daemon/k8s"
	"github.com/cilium/cilium/pkg/ciliumenvoyconfig/types"
	"github.com/cilium/cilium/pkg/envoy"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/synced"
	k8stestutils "github.com/cilium/cilium/pkg/k8s/testutils"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	"github.com/cilium/cilium/pkg/metrics"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/promise"
)

var updateFlag = flag.Bool("update", false, "Update txtar files")

func TestScript(t *testing.T) {
	const tctxKey = "tctx"
	type testContext struct {
		db   *statedb.DB
		cecs statedb.Table[*types.CEC]

		fakeEnvoy   *fakeEnvoySyncer
		fakeTrigger *fakePolicyTrigger
	}

	resourcesSummary := func(count int32, res *envoy.Resources) string {
		if res == nil {
			return fmt.Sprintf("%d <nil>", count)
		}
		var listeners, endpoints []string
		for _, l := range res.Listeners {
			listeners = append(listeners,
				fmt.Sprintf("%s/%d", l.Name, l.Address.GetSocketAddress().GetPortValue()))
		}
		sort.Strings(listeners)
		for _, cla := range res.Endpoints {
			for _, eps := range cla.Endpoints {
				backends := make([]string, 0, len(eps.LbEndpoints))
				for _, lep := range eps.LbEndpoints {
					ep := lep.GetEndpoint()
					sa := ep.Address.GetSocketAddress()
					backends = append(backends, fmt.Sprintf("%s:%d", sa.Address, sa.GetPortValue()))
				}
				endpoints = append(endpoints, cla.ClusterName+"="+strings.Join(backends, ","))
			}
		}
		sort.Strings(endpoints)
		return fmt.Sprintf("%d L:%s EP:%s", count, strings.Join(listeners, ","), strings.Join(endpoints, ","))
	}

	cmpEnvoy := func(kind string) statedbtest.Cmd {
		return func(ts *testscript.TestScript, neg bool, args []string) {
			if len(args) < 1 {
				ts.Fatalf("bad args")
			}
			tctx := ts.Value(tctxKey).(*testContext)
			var actual string
			expected := strings.Join(args, " ")
			for range 50 {
				var count int32
				var res *envoy.Resources
				switch kind {
				case "update":
					count, res = tctx.fakeEnvoy.update.load()
				case "upsert":
					count, res = tctx.fakeEnvoy.upsert.load()
				case "delete":
					count, res = tctx.fakeEnvoy.delete.load()
				}

				actual = resourcesSummary(count, res)
				if actual == expected {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			ts.Fatalf("expected %q, got %q", expected, actual)
		}
	}

	commands := map[string]statedbtest.Cmd{
		"cmp_envoy_upsert": cmpEnvoy("upsert"),
		"cmp_envoy_update": cmpEnvoy("update"),
		"cmp_envoy_delete": cmpEnvoy("delete"),

		"node": func(ts *testscript.TestScript, neg bool, args []string) {
			if len(args) < 1 {
				ts.Fatalf("node (show|update) ...")
			}
			lns := ts.Value("localnodestore").(*node.LocalNodeStore)
			switch args[0] {
			case "show":
				n, err := lns.Get(context.TODO())
				if err != nil {
					ts.Fatalf("LocalNodeStore.Get: %s", err)
				}
				b, err := yaml.Marshal(n)
				if err != nil {
					ts.Fatalf("Marshal(Node): %s", err)
				}
				ts.Logf("%s", b)

			case "update":
				if len(args) != 2 {
					ts.Fatalf("node update <file>")
				}
				b := ts.ReadFile(args[1])
				var err error
				lns.Update(func(n *node.LocalNode) {
					err = yaml.Unmarshal([]byte(b), n)
				})
				if err != nil {
					ts.Fatalf("Unmarshal(%s) failed: %s", args[1], err)
				}
			}
		},

		"metrics": metrics.DumpMetricsCmd,

		"k8s": k8stestutils.K8sCommand,
	}

	// Add load-balancer script commands.
	maps.Insert(
		commands,
		maps.All(experimental.TestScriptCommands),
	)

	// Add StateDB commands
	maps.Insert(
		commands,
		maps.All(statedbtest.Commands),
	)

	setup := func(e *testscript.Env) error {
		var tctx testContext

		tctx.fakeEnvoy = &fakeEnvoySyncer{}
		tctx.fakeTrigger = &fakePolicyTrigger{}

		hive := hive.New(
			cell.Provide(func() *testscript.Env { return e }),

			cell.Invoke(statedbtest.Setup),

			metrics.TestCell,
			cell.Invoke(metrics.SetupTestScript),

			client.FakeClientCell,
			cell.Invoke(k8stestutils.SetupK8sCommand),

			daemonk8s.ResourcesCell,

			experimental.TestCell,
			node.LocalNodeStoreCell,

			cell.Module("cec-test", "test",
				cell.Group(
					cell.Provide(
						newCECResourceParser,
						func() PortAllocator { return staticPortAllocator{} },
					),
				),

				experimentalTableCells,
				experimentalControllerCells,

				cell.ProvidePrivate(
					func() promise.Promise[synced.CRDSync] {
						r, p := promise.New[synced.CRDSync]()
						r.Resolve(synced.CRDSync{})
						return p
					},
					cecListerWatchers,
					func() resourceMutator { return tctx.fakeEnvoy },
					func() policyTrigger { return tctx.fakeTrigger },
				),

				cell.Invoke(
					func(db *statedb.DB, lns *node.LocalNodeStore, reg *metrics.Registry, cecs statedb.Table[*types.CEC], w *experimental.Writer) {
						tctx.db = db
						tctx.cecs = cecs
						e.Values["localnodestore"] = lns
					},
					experimental.TestScriptCommandsSetup,
				),
			),
		)

		log := hivetest.Logger(t)
		require.NoError(t, hive.Start(log, context.TODO()), "Start")
		e.Defer(func() {
			hive.Stop(log, context.TODO())
		})

		e.Values[tctxKey] = &tctx
		return nil
	}

	testscript.Run(t, testscript.Params{
		Dir:           "testdata",
		Setup:         setup,
		Cmds:          commands,
		UpdateScripts: *updateFlag,
	})
}

type resourceStore struct {
	count atomic.Int32
	res   atomic.Pointer[envoy.Resources]
}

func (r *resourceStore) load() (int32, *envoy.Resources) {
	return r.count.Load(), r.res.Load()
}

func (r *resourceStore) store(res *envoy.Resources) {
	r.count.Add(1)
	r.res.Store(res)
}

type fakeEnvoySyncer struct {
	update, upsert, delete resourceStore
}

// DeleteResources implements envoySyncer.
func (f *fakeEnvoySyncer) DeleteEnvoyResources(ctx context.Context, res envoy.Resources) error {
	f.delete.store(&res)
	return nil
}

// UpdateResources implements envoySyncer.
func (f *fakeEnvoySyncer) UpdateEnvoyResources(ctx context.Context, old envoy.Resources, new envoy.Resources) error {
	f.update.store(&new)
	return nil
}

// UpsertResources implements envoySyncer.
func (f *fakeEnvoySyncer) UpsertEnvoyResources(ctx context.Context, res envoy.Resources) error {
	f.upsert.store(&res)
	return nil
}

var _ resourceMutator = &fakeEnvoySyncer{}

type fakePolicyTrigger struct {
	count atomic.Int32
}

// TriggerPolicyUpdates implements policyTrigger.
func (f *fakePolicyTrigger) TriggerPolicyUpdates() {
	f.count.Add(1)
}

var _ policyTrigger = &fakePolicyTrigger{}

type staticPortAllocator struct{}

// AckProxyPort implements PortAllocator.
func (s staticPortAllocator) AckProxyPort(ctx context.Context, name string) error {
	return nil
}

// AllocateCRDProxyPort implements PortAllocator.
func (s staticPortAllocator) AllocateCRDProxyPort(name string) (uint16, error) {
	return 1000, nil
}

// ReleaseProxyPort implements PortAllocator.
func (s staticPortAllocator) ReleaseProxyPort(name string) error {
	return nil
}

var _ PortAllocator = staticPortAllocator{}
