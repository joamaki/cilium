// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package redirectpolicy

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"
	"testing"
	"text/tabwriter"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/statedb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_discoveryv1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1"
	"github.com/cilium/cilium/pkg/k8s/testutils"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	"github.com/cilium/cilium/pkg/time"
)

func TestLRPController(t *testing.T) {
	serviceFiles := []string{
		"testdata/service.yaml",
		"testdata/service2.yaml",
	}
	lrpFiles := []string{
		"testdata/lrp_addr.yaml",
		"testdata/lrp_svc.yaml",
	}
	podFiles := []string{
		"testdata/pod.yaml",
	}
	endpointSliceFiles := []string{
		"testdata/endpointslice.yaml",
		"testdata/endpointslice2.yaml",
	}

	parseEndpoints := func(obj any) (*k8s.Endpoints, bool) {
		return k8s.ParseEndpointSliceV1(obj.(*slim_discoveryv1.EndpointSlice)), true
	}

	// Test first that the test data can be decoded.
	for _, files := range [][]string{serviceFiles, lrpFiles, podFiles} {
		for _, file := range files {
			_, err := testutils.DecodeFile(file)
			require.NoError(t, err, "Decode: "+file)
		}
	}

	lrpLW, podLW := testutils.NewFakeListerWatcher(), testutils.NewFakeListerWatcher()
	log := hivetest.Logger(t, hivetest.LogLevel(slog.LevelDebug))

	var (
		db       *statedb.DB
		writer   *experimental.Writer
		lrpTable statedb.Table[*LRPConfig]
	)

	hive := hive.New(
		cell.Module("test", "test",
			experimental.TablesCell,
			experimental.ReflectorCell,

			cell.Provide(
				tables.NewNodeAddressTable,
				statedb.RWTable[tables.NodeAddress].ToTable,

				func() experimental.Config { return experimental.Config{EnableExperimentalLB: true} },
				func() experimental.ExternalConfig { return experimental.ExternalConfig{} },
				resource.EventStreamFromFiles[*slim_corev1.Service](serviceFiles),
				resource.EventStreamFromFiles[*slim_corev1.Pod](nil),
				resource.EventStreamFromFiles[*k8s.Endpoints](endpointSliceFiles, parseEndpoints),
			),

			experimentalCells,

			cell.ProvidePrivate(
				func() podListerWatcher {
					return podLW
				},
				func() lrpListerWatcher {
					return lrpLW
				},
			),

			cell.Invoke(
				statedb.RegisterTable[tables.NodeAddress],
				func(db_ *statedb.DB, w *experimental.Writer, lrpTable_ statedb.Table[*LRPConfig]) {
					db = db_
					writer = w
					lrpTable = lrpTable_
				},
			),
		),
	)

	dumpTables := func() []byte {
		var tableBuf bytes.Buffer
		writer.DebugDump(db.ReadTxn(), &tableBuf)
		tw := tabwriter.NewWriter(&tableBuf, 5, 0, 3, ' ', 0)
		fmt.Fprintln(tw, "\n--- LRPs ---")
		fmt.Fprintln(tw, strings.Join((*LRPConfig)(nil).TableHeader(), "\t"))
		iter := lrpTable.All(db.ReadTxn())
		for svc, _, ok := iter.Next(); ok; svc, _, ok = iter.Next() {
			fmt.Fprintln(tw, strings.Join(svc.TableRow(), "\t"))
		}
		tw.Flush()
		return experimental.SanitizeTableDump(tableBuf.Bytes())
	}

	assertTables := func(suffix string) {
		expectedFile := "expected" + suffix + ".tables"
		actualFile := "actual" + suffix + ".tables"

		var expectedTables []byte
		if expectedData, err := os.ReadFile(path.Join("testdata", expectedFile)); err == nil {
			expectedTables = expectedData
		}
		var lastActual []byte
		if !assert.Eventually(
			t,
			func() bool {
				lastActual = dumpTables()
				return bytes.Equal(lastActual, expectedTables)
			},
			time.Second,
			10*time.Millisecond) {
			os.WriteFile(path.Join("testdata", actualFile), lastActual, 0644)
			t.Errorf("Mismatching tables:\n%s:\n%s\n%s:\n%s",
				expectedFile,
				string(expectedTables),
				actualFile,
				string(lastActual))
		}
	}

	// --------------------------

	require.NoError(t, hive.Start(log, context.TODO()), "Start")

	for _, f := range podFiles {
		require.NoError(
			t,
			podLW.UpsertFromFile(f),
			"Upsert "+f,
		)
	}

	assertTables("_before")

	for _, f := range lrpFiles {
		require.NoError(
			t,
			lrpLW.UpsertFromFile(f),
			"Upsert "+f,
		)
	}

	assertTables("")

	for _, f := range lrpFiles {
		require.NoError(
			t,
			lrpLW.DeleteFromFile(f),
			"Delete "+f,
		)
	}

	assertTables("_after")

	assert.NoError(t, hive.Stop(log, context.TODO()), "Stop")
}
