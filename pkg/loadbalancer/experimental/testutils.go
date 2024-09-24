// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"context"
	"os"
	"strings"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/reconciler"
	statedbtest "github.com/cilium/statedb/testutils"
	"github.com/cilium/stream"
	"github.com/rogpeppe/go-internal/testscript"

	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_discovery_v1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1"
	slim_discovery_v1beta1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1beta1"
	k8stestutils "github.com/cilium/cilium/pkg/k8s/testutils"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/rate"
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

	cell.Provide(NewTestObjectFeeder),

	// Provide the tables and [Writer]
	TablesCell,

	// Add in the reflector to allow feeding in the inputs.
	ReflectorCell,

	ReconcilerCell,
)

type TestObjectFeeder struct {
	services  chan resource.Event[*slim_corev1.Service]
	endpoints chan resource.Event[*k8s.Endpoints]
	pods      chan resource.Event[*slim_corev1.Pod]
}

func (tof *TestObjectFeeder) UpsertService(svc *slim_corev1.Service) {
	tof.services <- resource.Event[*slim_corev1.Service]{
		Key:    resource.NewKey(svc),
		Object: svc,
		Kind:   resource.Upsert,
		Done:   func(error) {},
	}
}

func (tof *TestObjectFeeder) DeleteService(svc *slim_corev1.Service) {
	tof.services <- resource.Event[*slim_corev1.Service]{
		Key:    resource.NewKey(svc),
		Object: svc,
		Kind:   resource.Delete,
		Done:   func(error) {},
	}
}

func (tof *TestObjectFeeder) UpsertPod(svc *slim_corev1.Pod) {
	tof.pods <- resource.Event[*slim_corev1.Pod]{
		Key:    resource.NewKey(svc),
		Object: svc,
		Kind:   resource.Upsert,
		Done:   func(error) {},
	}
}

func (tof *TestObjectFeeder) DeletePod(svc *slim_corev1.Pod) {
	tof.pods <- resource.Event[*slim_corev1.Pod]{
		Key:    resource.NewKey(svc),
		Object: svc,
		Kind:   resource.Delete,
		Done:   func(error) {},
	}
}

func (tof *TestObjectFeeder) UpsertEndpoints(eps *k8s.Endpoints) {
	tof.endpoints <- resource.Event[*k8s.Endpoints]{
		Key:    resource.NewKey(eps),
		Object: eps,
		Kind:   resource.Upsert,
		Done:   func(error) {},
	}
}

func (tof *TestObjectFeeder) DeleteEndpoints(eps *k8s.Endpoints) {
	tof.endpoints <- resource.Event[*k8s.Endpoints]{
		Key:    resource.NewKey(eps),
		Object: eps,
		Kind:   resource.Delete,
		Done:   func(error) {},
	}
}

func NewTestObjectFeeder() (*TestObjectFeeder, StreamsOut) {
	var tof TestObjectFeeder
	tof.services = make(chan resource.Event[*slim_corev1.Service], 1)
	tof.services <- resource.Event[*slim_corev1.Service]{
		Kind: resource.Sync,
		Done: func(error) {},
	}
	tof.endpoints = make(chan resource.Event[*k8s.Endpoints], 1)
	tof.endpoints <- resource.Event[*k8s.Endpoints]{
		Kind: resource.Sync,
		Done: func(error) {},
	}
	tof.pods = make(chan resource.Event[*slim_corev1.Pod], 1)
	tof.pods <- resource.Event[*slim_corev1.Pod]{
		Kind: resource.Sync,
		Done: func(error) {},
	}
	out := StreamsOut{
		ServicesStream:  stream.FromChannel(tof.services),
		EndpointsStream: stream.FromChannel(tof.endpoints),
		PodsStream:      stream.FromChannel(tof.pods),
	}
	return &tof, out
}

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
	"upsert_service": func(ts *testscript.TestScript, neg bool, args []string) {
		obj, err := k8stestutils.DecodeObject([]byte(ts.ReadFile(args[0])))
		if err != nil {
			ts.Fatalf("DecodeObject: %s", err)
		}
		tof := ts.Value(tsTOFKey).(*TestObjectFeeder)
		tof.UpsertService(obj.(*slim_corev1.Service))
	},
	"delete_service": func(ts *testscript.TestScript, neg bool, args []string) {
		obj, err := k8stestutils.DecodeObject([]byte(ts.ReadFile(args[0])))
		if err != nil {
			ts.Fatalf("DecodeObject: %s", err)
		}
		tof := ts.Value(tsTOFKey).(*TestObjectFeeder)
		tof.DeleteService(obj.(*slim_corev1.Service))
	},

	"upsert_endpoints": func(ts *testscript.TestScript, neg bool, args []string) {
		obj, err := k8stestutils.DecodeObject([]byte(ts.ReadFile(args[0])))
		if err != nil {
			ts.Fatalf("DecodeObject: %s", err)
		}
		tof := ts.Value(tsTOFKey).(*TestObjectFeeder)
		var eps *k8s.Endpoints
		switch obj := obj.(type) {
		case *slim_corev1.Endpoints:
			eps = k8s.ParseEndpoints(obj)
		case *slim_discovery_v1.EndpointSlice:
			eps = k8s.ParseEndpointSliceV1(obj)
		case *slim_discovery_v1beta1.EndpointSlice:
			eps = k8s.ParseEndpointSliceV1Beta1(obj)
		default:
			ts.Fatalf("Unknown type %T", obj)
		}
		tof.UpsertEndpoints(eps)
	},
	"delete_endpoints": func(ts *testscript.TestScript, neg bool, args []string) {
		obj, err := k8stestutils.DecodeObject([]byte(ts.ReadFile(args[0])))
		if err != nil {
			ts.Fatalf("DecodeObject: %s", err)
		}
		tof := ts.Value(tsTOFKey).(*TestObjectFeeder)
		var eps *k8s.Endpoints
		switch obj := obj.(type) {
		case *slim_corev1.Endpoints:
			eps = k8s.ParseEndpoints(obj)
		case *slim_discovery_v1.EndpointSlice:
			eps = k8s.ParseEndpointSliceV1(obj)
		case *slim_discovery_v1beta1.EndpointSlice:
			eps = k8s.ParseEndpointSliceV1Beta1(obj)
		default:
			ts.Fatalf("Unknown type %T", obj)
		}
		tof.DeleteEndpoints(eps)
	},

	"write_maps": func(ts *testscript.TestScript, neg bool, args []string) {
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
	},

	"wait_for_reconciliation": func(ts *testscript.TestScript, neg bool, args []string) {
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
	},

	"show_maps": func(ts *testscript.TestScript, neg bool, args []string) {
		maps := ts.Value(tsLBMapsKey).(LBMaps)
		actualMaps := DumpLBMaps(maps, loadbalancer.L3n4Addr{}, false, nil)
		for _, line := range actualMaps {
			ts.Logf("%s", line)
		}
	},
}

func TestScriptCommandsSetup(e *testscript.Env) func(db *statedb.DB, w *Writer, tof *TestObjectFeeder, lbmaps LBMaps) {
	return func(db *statedb.DB, w *Writer, tof *TestObjectFeeder, lbmaps LBMaps) {
		e.Values[tsDBKey] = db
		e.Values[tsWriterKey] = w
		e.Values[tsTOFKey] = tof
		e.Values[tsLBMapsKey] = lbmaps
	}
}

func registerFakeReconciliation(jg job.Group, w *Writer) error {
	db := w.db
	fes := w.Frontends().(statedb.RWTable[*Frontend])
	jg.Add(job.OneShot(
		"fake-reconciler",
		func(ctx context.Context, health cell.Health) error {
			lastRevision := statedb.Revision(0)
			limiter := rate.NewLimiter(10*time.Millisecond, 5)
			for {
				limiter.Wait(ctx)
				wtxn := db.WriteTxn(fes)
				updates, watch := fes.LowerBoundWatch(wtxn, statedb.ByRevision[*Frontend](lastRevision+1))
				for fe, rev := range updates {
					lastRevision = rev
					if fe.Status.Kind == reconciler.StatusKindPending {
						fe = fe.Clone()
						fe.setStatus(reconciler.StatusDone())
						fes.Insert(wtxn, fe)
					}
				}
				wtxn.Commit()
				select {
				case <-ctx.Done():
					return nil
				case <-watch:
				}
			}
		},
	))
	return nil
}
