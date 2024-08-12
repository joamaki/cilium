// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package ciliumenvoyconfig

import (
	"context"
	"log/slog"
	"testing"
	"time"

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
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	"github.com/cilium/cilium/pkg/node"
)

func TestCECController(t *testing.T) {
	serviceFiles := []string{
		"testdata/experimental/service.yaml",
	}
	cecFiles := []string{
		"testdata/experimental/ciliumenvoyconfig.yaml",
	}
	endpointSliceFiles := []string{
		"testdata/experimental/endpointslice.yaml",
	}

	parseEndpoints := func(obj any) (*k8s.Endpoints, bool) {
		return k8s.ParseEndpointSliceV1(obj.(*slim_discoveryv1.EndpointSlice)), true
	}
	// Test first that the test data can be decoded.
	for _, files := range [][]string{serviceFiles, cecFiles} {
		for _, file := range files {
			_, err := testutils.DecodeFile(file)
			require.NoError(t, err, "Decode: "+file)
		}
	}

	cecLW, ccecLW := testutils.NewFakeListerWatcher(), testutils.NewFakeListerWatcher()
	log := hivetest.Logger(t, hivetest.LogLevel(slog.LevelDebug))

	var (
		db     *statedb.DB
		writer *experimental.Writer
	)

	hive := hive.New(
		// cecResourceParser and its friends.
		cell.Group(
			cell.Provide(
				newCECResourceParser,
				func() PortAllocator { return NewMockPortAllocator() },
			),
			node.LocalNodeStoreCell,
		),

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

			experimentalTableCells,
			experimentalControllerCells,

			cell.ProvidePrivate(
				func() listerWatchers {
					return listerWatchers{
						cec:  cecLW,
						ccec: ccecLW,
					}
				},
			),

			cell.Invoke(
				statedb.RegisterTable[tables.NodeAddress],
				func(db_ *statedb.DB, w *experimental.Writer) {
					db = db_
					writer = w
				},
			),
		),
	)

	require.NoError(t, hive.Start(log, context.TODO()), "Start")

	require.NoError(
		t,
		ccecLW.UpsertFromFile("testdata/experimental/ciliumenvoyconfig.yaml"),
		"Upsert ciliumenvoyconfig.yaml",
	)

	assert.Eventually(
		t,
		func() bool {
			svc, _, found := writer.Services().Get(
				db.ReadTxn(),
				experimental.ServiceByName(loadbalancer.ServiceName{Namespace: "test", Name: "echo"}),
			)
			if !found {
				return false
			}
			return svc.L7ProxyPort != 0
		},
		time.Second,
		10*time.Millisecond,
	)

	require.NoError(t, hive.Stop(log, context.TODO()), "Stop")
}
