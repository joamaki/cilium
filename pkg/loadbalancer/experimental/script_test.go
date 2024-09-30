// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental_test

import (
	"context"
	"flag"
	"maps"
	"testing"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	statedbtest "github.com/cilium/statedb/testutils"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/require"

	daemonk8s "github.com/cilium/cilium/daemon/k8s"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/k8s/client"
	k8stestutils "github.com/cilium/cilium/pkg/k8s/testutils"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	"github.com/cilium/cilium/pkg/metrics"
)

var update = flag.Bool("update", false, "update txtar scripts")

func TestScript(t *testing.T) {
	setup := func(e *testscript.Env) error {
		hive := hive.New(
			// Test scaffolding.
			cell.Group(
				cell.Provide(func() *testscript.Env { return e }),

				client.FakeClientCell,
				cell.Invoke(k8stestutils.SetupK8sCommand),

				daemonk8s.ResourcesCell,

				metrics.TestCell,
				cell.Invoke(metrics.SetupTestScript),

				cell.Invoke(statedbtest.Setup),

				cell.Invoke(experimental.TestScriptCommandsSetup),
			),

			// Our test target: load-balancing tables, k8s reflector and BPF reconciler.
			experimental.TestCell,
		)
		log := hivetest.Logger(t)
		require.NoError(t, hive.Start(log, context.TODO()), "Start")
		e.Defer(func() {
			hive.Stop(log, context.TODO())
		})
		return nil
	}

	cmds := maps.Clone(experimental.TestScriptCommands)
	cmds["k8s"] = k8stestutils.K8sCommand
	cmds["metrics"] = metrics.DumpMetricsCmd
	maps.Insert(cmds, maps.All(statedbtest.Commands))

	testscript.Run(t, testscript.Params{
		Dir:           "testdata/txtar",
		Setup:         setup,
		Cmds:          cmds,
		UpdateScripts: *update,
	})
}
