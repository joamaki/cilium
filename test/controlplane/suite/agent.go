// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package suite

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	"github.com/cilium/cilium/daemon/cmd"
	"github.com/cilium/cilium/pkg/datapath"
	fakeDatapath "github.com/cilium/cilium/pkg/datapath/fake"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	agentOption "github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/promise"
)

type agentHandle struct {
	t       *testing.T
	d       *cmd.Daemon
	tempDir string

	hive *hive.Hive
}

func (h *agentHandle) tearDown() {
	if err := h.hive.Stop(context.TODO()); err != nil {
		h.t.Fatalf("Failed to stop the agent: %s", err)
	}
	os.RemoveAll(h.tempDir)
}

func startCiliumAgent(t *testing.T, nodeName string, clientset k8sClient.Clientset) (*fakeDatapath.FakeDatapath, agentHandle, error) {
	var (
		err           error
		handle        agentHandle
		daemonPromise promise.Promise[*cmd.Daemon]
	)

	handle.t = t
	handle.tempDir = setupTestDirectories()
	fdp := fakeDatapath.NewDatapath()

	handle.hive = hive.New(
		// Provide the mocked infrastructure and datapath components
		cell.Provide(
			func() k8sClient.Clientset { return clientset },
			func() datapath.Datapath { return fdp },
		),

		cmd.ControlPlane,

		cell.Invoke(func(p promise.Promise[*cmd.Daemon]) {
			daemonPromise = p
		}),
	)

	if err := handle.hive.Start(context.TODO()); err != nil {
		return nil, agentHandle{}, err
	}

	handle.d, err = daemonPromise.Await(context.TODO())
	return fdp, handle, err
}

func setupTestDirectories() string {
	tempDir, err := ioutil.TempDir("", "cilium-test-")
	if err != nil {
		panic(fmt.Sprintf("TempDir() failed: %s", err))
	}
	agentOption.Config.RunDir = tempDir
	agentOption.Config.StateDir = tempDir
	return tempDir
}
