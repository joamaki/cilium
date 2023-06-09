package mtu_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/cilium/cilium/pkg/datapath/mtu"
	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/stream"
)

// tableInitFunc is a function called to initialize device and route
// tables before MTU module starts.
type tableInitFunc func(
	devices statedb.TableReaderWriter[*tables.Device],
	routes statedb.TableReaderWriter[*tables.Route])

type testContext struct {
	mtu     types.MTU
	db      statedb.DB
	devices statedb.Table[*tables.Device]
	routes  statedb.Table[*tables.Route]
}

type testFunc func(testContext)

func doTest(t *testing.T, init tableInitFunc, test testFunc) {
	// Catch leaked goroutines.
	defer goleak.VerifyNone(t)

	var testCtx testContext
	h := hive.New(
		mtu.Cell,
		statedb.Cell,
		tables.Cell,
		job.Cell,

		// Initialize tables (if the test needs to)
		cell.Invoke(
			func(
				db statedb.DB,
				devicesT statedb.Table[*tables.Device],
				routesT statedb.Table[*tables.Route],
			) {
				if init != nil {
					txn := db.WriteTxn()
					defer txn.Commit()
					init(devicesT.Writer(txn), routesT.Writer(txn))
				}
			}),

		// Construct the test context.
		cell.Invoke(
			func(m types.MTU, db statedb.DB,
				devs statedb.Table[*tables.Device], r statedb.Table[*tables.Route]) {
				testCtx = testContext{m, db, devs, r}
			}),
	)
	require.NoError(t, h.Start(context.TODO()))
	test(testCtx)
	require.NoError(t, h.Stop(context.TODO()))
}

func TestMTU_RouteAndDeviceExist(t *testing.T) {
	// Test that no default MTU is emitted when a default route exists along
	// with the associated device.
	testMTU := 1234
	doTest(t,
		func(devices statedb.TableReaderWriter[*tables.Device],
			routes statedb.TableReaderWriter[*tables.Route]) {

			devices.Insert(&tables.Device{
				Viable: true,
				Addrs:  nil,
				Interface: net.Interface{
					Index: 123,
					Name:  "test-device",
					MTU:   testMTU,
				},
			})

			routes.Insert(&tables.Route{
				Table:     245,
				LinkName:  "test-device",
				LinkIndex: 123,
				Scope:     0,
				Dst:       nil,
				Src:       []byte{1, 2, 3, 4},
				Gw:        []byte{2, 3, 4, 5},
			})
		},
		func(tctx testContext) {
			mtu, err := stream.First[int](context.TODO(), tctx.mtu)
			if assert.NoError(t, err) {
				assert.Equal(t, mtu, testMTU)
			}
		})
}

func TestMTU_NoRoute(t *testing.T) {
	// Test that the default MTU is picked if the system does not yet have
	// a default route. Further test that once the default route and its
	// device are added we pick the new MTU.
	doTest(t,
		nil,
		func(tctx testContext) {
			ctx, cancel := context.WithCancel(context.Background())

			// With empty routes and devices we pick the default.
			mtus := stream.ToChannel[int](ctx, tctx.mtu)
			assert.Equal(t, <-mtus, mtu.DefaultMTU)

			// With device and default route we pick the device MTU
			testMTU := 2345
			txn := tctx.db.WriteTxn()
			tctx.devices.Writer(txn).Insert(&tables.Device{
				Viable: true,
				Addrs:  nil,
				Interface: net.Interface{
					Index: 123,
					Name:  "test-device",
					MTU:   testMTU,
				},
			})
			tctx.routes.Writer(txn).Insert(&tables.Route{
				Table:     245,
				LinkName:  "test-device",
				LinkIndex: 123,
				Scope:     0,
				Dst:       nil,
				Src:       []byte{1, 2, 3, 4},
				Gw:        []byte{2, 3, 4, 5},
			})
			txn.Commit()
			assert.Equal(t, <-mtus, testMTU)

			// When default route is removed we go back to the fallback
			// default MTU.
			txn = tctx.db.WriteTxn()
			tctx.routes.Writer(txn).DeleteAll(statedb.All)
			txn.Commit()
			assert.Equal(t, <-mtus, mtu.DefaultMTU)

			// Cleanup
			cancel()
			for range mtus {
			}

		})
}
