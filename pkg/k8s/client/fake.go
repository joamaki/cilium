// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package client

import (
	"errors"
	"fmt"
	"os"
	"strings"

	cilium_fake "github.com/cilium/cilium/pkg/k8s/client/clientset/versioned/fake"
	slim_clientset "github.com/cilium/cilium/pkg/k8s/slim/k8s/client/clientset/versioned"
	slim_fake "github.com/cilium/cilium/pkg/k8s/slim/k8s/client/clientset/versioned/fake"
	"github.com/cilium/cilium/pkg/k8s/testutils"
	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/script"
	"github.com/sirupsen/logrus"
	apiext_fake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	"k8s.io/apimachinery/pkg/api/meta"
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
		func(cfg SharedConfig) (*FakeClientset, Clientset) {
			fc, _ := NewFakeClientset()
			if !cfg.EnableK8s {
				fc.Disable()
			}
			return fc, fc
		},
		func(fc *FakeClientset) hive.ScriptCmdOut {
			return hive.NewScriptCmd("k8s", FakeClientCommand(fc))
		},
	),
)

type (
	MCSAPIFakeClientset     = mcsapi_fake.Clientset
	KubernetesFakeClientset = fake.Clientset
	SlimFakeClientset       = slim_fake.Clientset
	CiliumFakeClientset     = cilium_fake.Clientset
	APIExtFakeClientset     = apiext_fake.Clientset
)

type FakeClientset struct {
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
