// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"

	cilium_fake "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned/fake"
	slim_clientset "github.com/cilium/cilium/pkg/k8s/slim/k8s/client/clientset/versioned"
	slim_fake "github.com/cilium/cilium/pkg/k8s/slim/k8s/client/clientset/versioned/fake"
	"github.com/cilium/cilium/pkg/k8s/testutils"
	"github.com/cilium/cilium/pkg/k8s/version"
	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/hive/script"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	apiext_fake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	versionapi "k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8sTesting "k8s.io/client-go/testing"
	mcsapi_fake "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned/fake"
)

var FakeClientCell = cell.Module(
	"k8s-fake-client",
	"Fake Kubernetes client",

	cell.Config(defaultSharedConfig),

	cell.Provide(
		newFakeClientsetForHive,
		func(fc *FakeClientset) hive.ScriptCmdOut {
			return hive.NewScriptCmd("k8s", FakeClientCommand(fc))
		},
	),
)

func newFakeClientsetForHive(jg job.Group, log *slog.Logger, cfg SharedConfig) (*FakeClientset, Clientset) {
	fc, _ := NewFakeClientset()
	fc.cfg = cfg
	fc.log = log
	if !cfg.EnableK8s {
		fc.Disable()
		return fc, fc
	}

	version.Force("1.31.0")

	if cfg.K8sFakeObjectsPath != "" {
		// FIXME: Need to synchronize with informers/reflectors before we can start feeding
		// the trackers! This is non-trivial as we don't have a way of knowning the full
		// set of them.
		jg.Add(
			job.OneShot("directory-watcher", fc.watchLoop,
				job.WithRetry(-1, &job.ExponentialBackoff{Min: time.Second, Max: time.Second}),
			))
	}
	return fc, fc
}

type (
	MCSAPIFakeClientset     = mcsapi_fake.Clientset
	KubernetesFakeClientset = fake.Clientset
	SlimFakeClientset       = slim_fake.Clientset
	CiliumFakeClientset     = cilium_fake.Clientset
	APIExtFakeClientset     = apiext_fake.Clientset
)

type FakeClientset struct {
	cfg SharedConfig
	log *slog.Logger

	disabled bool

	*MCSAPIFakeClientset
	*KubernetesFakeClientset
	*CiliumFakeClientset
	*APIExtFakeClientset
	clientsetGetters

	SlimFakeClientset *SlimFakeClientset

	trackers map[string]k8sTesting.ObjectTracker

	enabled bool
}

var _ Clientset = &FakeClientset{}

func (c *FakeClientset) Slim() slim_clientset.Interface {
	return c.SlimFakeClientset
}

func (c *FakeClientset) Discovery() discovery.DiscoveryInterface {
	return c.KubernetesFakeClientset.Discovery()
}

func (c *FakeClientset) IsEnabled() bool {
	return c != nil && !c.disabled
}

func (c *FakeClientset) Disable() {
	c.disabled = true
}

func (c *FakeClientset) Config() Config {
	//exhaustruct:ignore
	return Config{}
}

func (c *FakeClientset) RestConfig() *rest.Config {
	//exhaustruct:ignore
	return &rest.Config{}
}

func (fc *FakeClientset) watchLoop(ctx context.Context, health cell.Health) error {
	dir := fc.cfg.K8sFakeObjectsPath
	if dir == "" {
		return nil
	}

	stat, err := os.Stat(dir)
	if err == nil && !stat.IsDir() {
		err = fmt.Errorf("%q is not a directory", dir)
	}
	if err != nil {
		return fmt.Errorf("invalid --k8s-fake-objects-path: %w", err)
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return fmt.Errorf("failed to watch %q: %w", dir, err)
	}

	events := watcher.Events

	// Synthesize creates for the existing files.
	go func() {
	loop:
		for _, ent := range ents {
			select {
			case events <- fsnotify.Event{
				Op:   fsnotify.Create,
				Name: path.Join(dir, ent.Name()),
			}:
			case <-ctx.Done():
				break loop
			}
		}
		<-ctx.Done()
		watcher.Close()
	}()

	fileToIdentity := map[string]objectIdentity{}

	health.OK(fmt.Sprintf("Watching %q", dir))

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-events:
			switch ev.Op {
			case fsnotify.Create:
				_, exists := fileToIdentity[ev.Name]
				if exists {
					// Ignore double creations (ReadDir race)
					continue
				}
				fallthrough

			case fsnotify.Write:
				if path.Ext(ev.Name) != ".yaml" {
					continue
				}
				fc.log.Info("processing file", "file", ev.Name)
				id, err := fc.processFile(ev.Name, ev.Op == fsnotify.Create)
				if err != nil {
					fc.log.Error("failed to process file", "file", path.Join(dir, ev.Name), "error", err)
				} else {
					fileToIdentity[ev.Name] = id
				}
			case fsnotify.Remove:
				if id, found := fileToIdentity[ev.Name]; found {
					delete(fileToIdentity, ev.Name)
					if err := fc.deleteIdentity(id); err != nil {
						panic("TODO log")
					}
				}
			default:
				fc.log.Warn("unhandled event", "event", ev)
			}
		}
	}
}

type objectIdentity struct {
	gvr             schema.GroupVersionResource
	namespace, name string
}

func (fc *FakeClientset) processFile(file string, add bool) (id objectIdentity, err error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return id, fmt.Errorf("failed to read %s: %w", file, err)
	}
	obj, gvk, err := testutils.DecodeObjectGVK(b)
	if err != nil {
		return id, fmt.Errorf("decode: %w", err)
	}
	gvr, _ := meta.UnsafeGuessKindToResource(*gvk)
	objMeta, err := meta.Accessor(obj)
	if err != nil {
		return id, fmt.Errorf("accessor: %w", err)
	}
	name := objMeta.GetName()
	ns := objMeta.GetNamespace()
	id = objectIdentity{gvr, ns, name}

	// Try to add the object to all the trackers. If one of them
	// accepts we're good. We'll add to all since multiple trackers
	// may accept (e.g. slim and kubernetes).

	// err will get set to nil if any of the tracker methods succeed.
	// start with a non-nil default error.
	err = fmt.Errorf("none of the trackers of FakeClientset accepted %T", obj)
	for trackerName, tracker := range fc.trackers {
		var trackerErr error
		if add {
			trackerErr = tracker.Add(obj)
		} else {
			trackerErr = tracker.Update(gvr, obj, ns)
		}
		if err != nil {
			if trackerErr == nil {
				// One of the trackers accepted the object, it's a success!
				err = nil
			} else {
				err = errors.Join(err, fmt.Errorf("%s: %w", trackerName, trackerErr))
			}
		}
	}
	return
}

func (fc *FakeClientset) deleteIdentity(id objectIdentity) error {
	err := fmt.Errorf("none of the trackers of FakeClientset accepted %v", id)
	for trackerName, tracker := range fc.trackers {
		trackerErr := tracker.Delete(id.gvr, id.namespace, id.name)
		if err != nil {
			if trackerErr == nil {
				// One of the trackers accepted the object, it's a success!
				err = nil
			} else {
				err = errors.Join(err, fmt.Errorf("%s: %w", trackerName, trackerErr))
			}
		}
	}
	return err
}

func NewFakeClientset() (*FakeClientset, Clientset) {
	version := testutils.DefaultVersion
	return NewFakeClientsetWithVersion(version)
}

func NewFakeClientsetWithVersion(version string) (*FakeClientset, Clientset) {
	if version == "" {
		version = testutils.DefaultVersion
	}
	resources, found := testutils.APIResources[version]
	if !found {
		panic("version " + version + " not found from testutils.APIResources")
	}

	client := FakeClientset{
		SlimFakeClientset:       slim_fake.NewSimpleClientset(),
		CiliumFakeClientset:     cilium_fake.NewSimpleClientset(),
		APIExtFakeClientset:     apiext_fake.NewSimpleClientset(),
		MCSAPIFakeClientset:     mcsapi_fake.NewSimpleClientset(),
		KubernetesFakeClientset: fake.NewSimpleClientset(),
		enabled:                 true,
	}
	client.KubernetesFakeClientset.Resources = resources
	client.SlimFakeClientset.Resources = resources
	client.CiliumFakeClientset.Resources = resources
	client.APIExtFakeClientset.Resources = resources
	client.trackers = map[string]k8sTesting.ObjectTracker{
		"slim":       client.SlimFakeClientset.Tracker(),
		"cilium":     client.CiliumFakeClientset.Tracker(),
		"mcs":        client.MCSAPIFakeClientset.Tracker(),
		"kubernetes": client.KubernetesFakeClientset.Tracker(),
		"apiexit":    client.APIExtFakeClientset.Tracker(),
	}

	fd := client.KubernetesFakeClientset.Discovery().(*fakediscovery.FakeDiscovery)
	fd.FakedServerVersion = toVersionInfo(version)

	client.clientsetGetters = clientsetGetters{&client}
	return &client, &client
}

func toVersionInfo(rawVersion string) *versionapi.Info {
	parts := strings.Split(rawVersion, ".")
	return &versionapi.Info{Major: parts[0], Minor: parts[1]}
}

type ClientBuilderFunc func(name string) (Clientset, error)

// NewClientBuilder returns a function that creates a new Clientset with the given
// name appended to the user agent, or returns an error if the Clientset cannot be
// created.
func NewClientBuilder(lc cell.Lifecycle, log logrus.FieldLogger, cfg Config) ClientBuilderFunc {
	return func(name string) (Clientset, error) {
		c, err := newClientsetForUserAgent(lc, log, cfg, name)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
}

var FakeClientBuilderCell = cell.Provide(FakeClientBuilder)

func FakeClientBuilder() ClientBuilderFunc {
	fc, _ := NewFakeClientset()
	return func(_ string) (Clientset, error) {
		return fc, nil
	}
}

func FakeClientCommand(fc *FakeClientset) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "interact with fake k8s client",
			Args:    "<command> args...",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("usage: k8s <command> files...\n<command> is one of add, update or delete.")
			}

			action := args[0]
			if len(args) < 2 {
				return nil, fmt.Errorf("usage: k8s %s files...", action)
			}

			for _, file := range args[1:] {
				b, err := os.ReadFile(s.Path(file))
				if err != nil {
					// Try relative to current directory, e.g. to allow reading "testdata/foo.yaml"
					b, err = os.ReadFile(file)
				}
				if err != nil {
					return nil, fmt.Errorf("failed to read %s: %w", file, err)
				}
				obj, gvk, err := testutils.DecodeObjectGVK(b)
				if err != nil {
					return nil, fmt.Errorf("decode: %w", err)
				}
				gvr, _ := meta.UnsafeGuessKindToResource(*gvk)
				objMeta, err := meta.Accessor(obj)
				if err != nil {
					return nil, fmt.Errorf("accessor: %w", err)
				}
				name := objMeta.GetName()
				ns := objMeta.GetNamespace()

				// Try to add the object to all the trackers. If one of them
				// accepts we're good. We'll add to all since multiple trackers
				// may accept (e.g. slim and kubernetes).

				// err will get set to nil if any of the tracker methods succeed.
				// start with a non-nil default error.
				err = fmt.Errorf("none of the trackers of FakeClientset accepted %T", obj)
				for trackerName, tracker := range fc.trackers {
					var trackerErr error
					switch action {
					case "add":
						trackerErr = tracker.Add(obj)
					case "update":
						trackerErr = tracker.Update(gvr, obj, ns)
					case "delete":
						trackerErr = tracker.Delete(gvr, ns, name)
					default:
						return nil, fmt.Errorf("unknown k8s action %q, expected 'add', 'update' or 'delete'", action)
					}
					if err != nil {
						if trackerErr == nil {
							// One of the trackers accepted the object, it's a success!
							err = nil
						} else {
							err = errors.Join(err, fmt.Errorf("%s: %w", trackerName, trackerErr))
						}
					}
				}
				if err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
}
