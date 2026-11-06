// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package tests

import (
	"context"
	"maps"
	"strconv"
	"testing"

	uhive "github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/hive/script"
	"github.com/cilium/hive/script/scripttest"
	"github.com/cilium/statedb"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cilium/cilium/pkg/hive"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client/testutils"
	"github.com/cilium/cilium/pkg/quota"
	quotacell "github.com/cilium/cilium/pkg/quota/cell"
	"github.com/cilium/cilium/pkg/time"
)

func TestScript(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	log := hivetest.Logger(t)
	scripttest.Test(t, ctx, func(t testing.TB, args []string) *script.Engine {
		h := hive.New(
			k8sClient.FakeClientCell(),
			quotacell.Cell,
			cell.Provide(func(db *statedb.DB, table statedb.Table[quota.ResourceQuota], accounting quota.Accounting) uhive.ScriptCmdsOut {
				return uhive.NewScriptCmds(map[string]script.Cmd{
					"quota/restore": quotaUsageCommand(db, table, accounting, true, true),
					"quota/reserve": quotaUsageCommand(db, table, accounting, true),
					"quota/release": quotaUsageCommand(db, table, accounting, false),
				})
			}),
			cell.Invoke(func(statedb.Table[quota.ResourceQuota]) {}),
		)

		flags := pflag.NewFlagSet("", pflag.ContinueOnError)
		h.RegisterFlags(flags)
		require.NoError(t, flags.Parse(args), "parse args")
		t.Cleanup(func() {
			assert.NoError(t, h.Stop(log, context.TODO()))
		})
		cmds, err := h.ScriptCommands(log)
		require.NoError(t, err, "ScriptCommands")
		maps.Insert(cmds, maps.All(script.DefaultCmds()))
		return &script.Engine{Cmds: cmds}
	}, []string{}, "testdata/*.txtar")
}

func quotaUsageCommand(db *statedb.DB, table statedb.Table[quota.ResourceQuota], accounting quota.Accounting, reserve bool, restore ...bool) script.Cmd {
	return script.Command(script.CmdUsage{Summary: "Reserve or release named quota usage in one transaction", Args: "namespace resource reservation [amount] ..."}, func(_ *script.State, args ...string) (script.WaitFunc, error) {
		step := 2
		if reserve {
			step = 3
		}
		if len(args) < 1+step || (len(args)-1)%step != 0 {
			return nil, script.ErrUsage
		}
		txn := db.WriteTxn(table)
		defer txn.Abort()
		for i := 1; i < len(args); i += step {
			resource, id := quota.ResourceName(args[i]), quota.ReservationID(args[i+1])
			var err error
			if reserve {
				amount, parseErr := strconv.ParseUint(args[i+2], 10, 64)
				if parseErr != nil {
					return nil, parseErr
				}
				if len(restore) > 0 && restore[0] {
					err = accounting.Restore(txn, args[0], resource, id, amount)
				} else {
					_, err = accounting.Reserve(txn, args[0], resource, id, amount)
				}
			} else {
				err = accounting.Release(txn, args[0], resource, id)
			}
			if err != nil {
				return nil, err
			}
		}
		txn.Commit()
		return nil, nil
	})
}
