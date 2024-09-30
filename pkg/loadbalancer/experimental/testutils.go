// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"os"
	"strings"

	"github.com/cilium/hive/cell"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/reconciler"
	statedbtest "github.com/cilium/statedb/testutils"
	"github.com/rogpeppe/go-internal/testscript"

	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/time"
)

// TestCell provides a cell for testing with the load-balancing Writer and tables.
var TestCell = cell.Module(
	"test",
	"Experimental load-balancing testing utilities",

	cell.Provide(
		func() Config {
			return Config{
				EnableExperimentalLB: true,
				RetryBackoffMin:      time.Millisecond,
				RetryBackoffMax:      time.Millisecond,
			}
		},
		func() ExternalConfig {
			return ExternalConfig{}
		},

		NewFakeLBMaps,
	),

	// Provide the tables and [Writer]
	TablesCell,

	// Add in the reflector to allow feeding in the inputs.
	ReflectorCell,

	ReconcilerCell,

	cell.ProvidePrivate(resourcesToStreams),
)

const (
	tsDBKey     = "lb_db"
	tsWriterKey = "lb_writer"
	tsTOFKey    = "lb_tof"
	tsLBMapsKey = "lb_lbmaps"
)

// TestScriptCommands are the testscript commands for comparing and showing
// the StateDB tables of the load-balancing control-plane.
// The [dbKey] and [writerKey] are the keys in the testscript values map
// for [*statedb.DB] and [*Writer].
var TestScriptCommands = map[string]statedbtest.Cmd{
	"lb": func(ts *testscript.TestScript, neg bool, args []string) {
		usage := func() {
			ts.Fatalf("usage: lb <command> args...\n<command> is one of: initialized, reconciled, showmaps, writemaps")
		}
		if len(args) < 1 {
			usage()
		}
		switch args[0] {
		case "writemaps":
			WriteMapsCmd(ts, neg, args[1:])
		case "showmaps":
			ShowMapsCmd(ts, neg, args[1:])
		case "initialized":
			WaitUntilInitializedCmd(ts, neg, args[1:])
		case "reconciled":
			WaitUntilReconciledCmd(ts, neg, args[1:])
		default:
			usage()
		}
	},
}

func WriteMapsCmd(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 1 {
		ts.Fatalf("expected filename")
	}
	maps := ts.Value(tsLBMapsKey).(LBMaps)
	actualMaps := DumpLBMaps(maps, loadbalancer.L3n4Addr{}, false, nil)
	path := ts.MkAbs(args[0])
	os.WriteFile(
		path,
		[]byte(strings.Join(actualMaps, "\n")+"\n"),
		0644,
	)
}

func WaitUntilInitializedCmd(ts *testscript.TestScript, neg bool, args []string) {
	// TODO add a db command for this?
	db := ts.Value(tsDBKey).(*statedb.DB)
	w := ts.Value(tsWriterKey).(*Writer)
	txn := db.ReadTxn()
	_, fe := w.Frontends().Initialized(txn)
	_, be := w.Backends().Initialized(txn)
	_, svc := w.Services().Initialized(txn)
	<-fe
	<-be
	<-svc
}

func WaitUntilReconciledCmd(ts *testscript.TestScript, neg bool, args []string) {
	db := ts.Value(tsDBKey).(*statedb.DB)
	w := ts.Value(tsWriterKey).(*Writer)
	var lastRev uint64
	notDone := map[loadbalancer.L3n4Addr]bool{}
	for range 50 {
		for fe, rev := range w.Frontends().LowerBound(db.ReadTxn(), statedb.ByRevision[*Frontend](lastRev)) {
			lastRev = rev
			if fe.Status.Kind != reconciler.StatusKindDone {
				notDone[fe.Address] = true
			} else {
				delete(notDone, fe.Address)
			}
		}
		if len(notDone) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func ShowMapsCmd(ts *testscript.TestScript, neg bool, args []string) {
	maps := ts.Value(tsLBMapsKey).(LBMaps)
	actualMaps := DumpLBMaps(maps, loadbalancer.L3n4Addr{}, false, nil)
	for _, line := range actualMaps {
		ts.Logf("%s", line)
	}
}

func TestScriptCommandsSetup(e *testscript.Env, db *statedb.DB, w *Writer, lbmaps LBMaps) {
	e.Values[tsDBKey] = db
	e.Values[tsWriterKey] = w
	e.Values[tsLBMapsKey] = lbmaps
}
