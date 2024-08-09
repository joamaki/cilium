package ciliumenvoyconfig

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/k8s"
	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/statedb"
	"github.com/cilium/stream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
)

type fakeLW struct {
	watcher *watch.FakeWatcher
	added   sets.Set[types.NamespacedName]
}

func newFakeLW() *fakeLW {
	return &fakeLW{
		watcher: watch.NewFake(),
		added:   sets.New[types.NamespacedName](),
	}
}

func getNamespacedName(obj runtime.Object) types.NamespacedName {
	m, err := meta.Accessor(obj)
	if err != nil {
		panic(err)
	}
	return types.NamespacedName{Namespace: m.GetNamespace(), Name: m.GetName()}
}

func (f *fakeLW) upsert(obj runtime.Object) {
	name := getNamespacedName(obj)
	if f.added.Has(name) {
		f.watcher.Modify(obj)
	} else {
		f.watcher.Add(obj)
		f.added.Insert(name)
	}
}

func (f *fakeLW) delete(obj runtime.Object) {
	f.watcher.Delete(obj)
	f.added.Delete(getNamespacedName(obj))
}

func (f *fakeLW) stop() {
	f.watcher.Stop()
}

func (f *fakeLW) upsertFromFile(path string) error {
	if obj, err := decodeFile(path); err == nil {
		f.upsert(obj)
		return nil
	} else {
		return err
	}
}

func (f *fakeLW) deleteFromFile(path string) error {
	if obj, err := decodeFile(path); err == nil {
		f.delete(obj)
		return nil
	} else {
		return err
	}
}

var emptyList = &v1.PartialObjectMetadataList{}

// List implements cache.ListerWatcher.
func (f *fakeLW) List(options v1.ListOptions) (runtime.Object, error) {
	return emptyList, nil

}

// Watch implements cache.ListerWatcher.
func (f *fakeLW) Watch(options v1.ListOptions) (watch.Interface, error) {
	return f.watcher, nil
}

var decoder runtime.Decoder

func init() {
	scheme := runtime.NewScheme()
	slim_corev1.AddToScheme(scheme)
	cilium_v2.AddToScheme(scheme)
	decoder = serializer.NewCodecFactory(scheme).UniversalDeserializer()
}

func decodeObject(bytes []byte) (runtime.Object, error) {
	obj, _, err := decoder.Decode(bytes, nil, nil)
	return obj, err
}

func decodeFile(path string) (runtime.Object, error) {
	bs, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeObject(bs)
}

func TestDecodeCiliumEnvoyConfig(t *testing.T) {
	bytes, err := os.ReadFile("testdata/experimental/ciliumenvoyconfig.yaml")
	require.NoError(t, err, "ReadFile")
	obj, err := decodeObject(bytes)
	require.NoError(t, err, "decodeCiliumV2")

	t.Logf("obj: %#v", obj)
}

func TestCECReflection(t *testing.T) {
	cecLW, ccecLW := newFakeLW(), newFakeLW()
	log := hivetest.Logger(t) //, hivetest.LogLevel(slog.LevelDebug))

	var (
		db  *statedb.DB
		tbl statedb.Table[*cec]
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
			experimentalTableCells,

			cell.ProvidePrivate(
				func() experimental.Config {
					return experimental.Config{EnableExperimentalLB: true}
				},
				func() listerWatchers {
					return listerWatchers{
						cec:  cecLW,
						ccec: ccecLW,
					}
				},
			),

			cell.Invoke(func(db_ *statedb.DB, tbl_ statedb.Table[*cec]) {
				db = db_
				tbl = tbl_
			}),
		),
	)

	require.NoError(t, hive.Start(log, context.TODO()), "Start")

	ccecLW.upsertFromFile("testdata/experimental/ciliumenvoyconfig.yaml")

	assert.Eventually(
		t,
		func() bool {
			return tbl.NumObjects(db.ReadTxn()) > 0
		},
		time.Second,
		10*time.Millisecond,
	)

	require.NoError(t, hive.Stop(log, context.TODO()), "Stop")
}

func TestLBReflection(t *testing.T) {
	serviceFiles := []string{
		"testdata/experimental/service.yaml",
	}

	var (
		db  *statedb.DB
		tbl statedb.Table[*experimental.Service]
	)

	log := hivetest.Logger(t)
	hive := hive.New(
		cell.Module("test", "test",
			experimental.TablesCell,
			experimental.ReflectorCell,

			cell.Provide(
				// TODO: Perhaps rework the writer to remove this dependency? E.g. manage the node addresses
				// either at the reconciliation level, or push the state into the writer from a separate job?
				// Goal being that the [experimental.TablesCell] is easy to use in tests without having to pull
				// all this crap in.
				tables.NewNodeAddressTable,
				statedb.RWTable[tables.NodeAddress].ToTable,

				func() experimental.Config { return experimental.Config{EnableExperimentalLB: true} },
				resourceEventStreamFromFiles[*slim_corev1.Service](serviceFiles),
				resourceEventStreamFromFiles[*slim_corev1.Pod](nil),
				resourceEventStreamFromFiles[*k8s.Endpoints](nil),
			),
			cell.Invoke(
				func(db_ *statedb.DB, w *experimental.Writer) {
					db = db_
					tbl = w.Services()
				},

				statedb.RegisterTable[tables.NodeAddress],
			),
		),
	)
	require.NoError(t, hive.Start(log, context.TODO()), "Start")

	assert.Eventually(
		t,
		func() bool {
			return tbl.NumObjects(db.ReadTxn()) > 0
		},
		time.Second,
		10*time.Millisecond,
	)

	require.NoError(t, hive.Stop(log, context.TODO()), "Stop")

}

func resourceEventStreamFromFiles[T runtime.Object](paths []string) func() stream.Observable[resource.Event[T]] {
	src := make(chan resource.Event[T], 1)
	src <- resource.Event[T]{
		Kind: resource.Sync,
		Done: func(error) {},
	}
	go func() {
		for _, path := range paths {
			rawObj, err := decodeFile(path)
			if err != nil {
				panic(err)
			}
			obj := rawObj.(T)
			src <- resource.Event[T]{
				Kind:   resource.Upsert,
				Object: obj,
				Done:   func(error) {},
			}
		}
	}()
	return func() stream.Observable[resource.Event[T]] { return stream.FromChannel(src) }
}

func TestCECController(t *testing.T) {
	serviceFiles := []string{
		"testdata/experimental/service.yaml",
	}
	cecLW, ccecLW := newFakeLW(), newFakeLW()
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
				resourceEventStreamFromFiles[*slim_corev1.Service](serviceFiles),
				resourceEventStreamFromFiles[*slim_corev1.Pod](nil),
				resourceEventStreamFromFiles[*k8s.Endpoints](nil),
			),

			experimentalTableCells,
			experimentalControllerCells,

			cell.ProvidePrivate(
				func() experimental.Config {
					return experimental.Config{EnableExperimentalLB: true}
				},
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

	ccecLW.upsertFromFile("testdata/experimental/ciliumenvoyconfig.yaml")

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
