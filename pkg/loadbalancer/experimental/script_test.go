// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental_test

import (
	"context"
	"flag"
	"testing"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/require"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
)

var update = flag.Bool("update", false, "update txtar scripts")

func TestScript(t *testing.T) {
	setup := func(e *testscript.Env) error {
		hive := hive.New(
			experimental.TestCell,
			cell.Invoke(experimental.TestScriptCommandsSetup(e)),
		)
		log := hivetest.Logger(t)
		require.NoError(t, hive.Start(log, context.TODO()), "Start")
		e.Defer(func() {
			hive.Stop(log, context.TODO())
		})
		return nil
	}

	testscript.Run(t, testscript.Params{
		Dir:           "testdata/txtar",
		Setup:         setup,
		Cmds:          experimental.TestScriptCommands,
		UpdateScripts: *update,
	})
}
