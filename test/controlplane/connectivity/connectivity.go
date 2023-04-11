// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package nodeport

import (
	"os"
	"path"
	"testing"
	"time"

	operatorOption "github.com/cilium/cilium/operator/option"
	agentOption "github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/test/controlplane/suite"
)

func init() {
	suite.AddTestCase("Connectivity", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}

		modConfig := func(daemonCfg *agentOption.DaemonConfig, _ *operatorOption.OperatorConfig) {
			daemonCfg.EnableNodePort = true
		}

		for _, version := range []string{"1.24"} /*controlplane.K8sVersions()*/ {
			abs := func(f string) string { return path.Join(cwd, "connectivity", "v"+version, f) }

			// Run the test from each nodes perspective.
			for _, nodeName := range []string{"connectivity-worker"} {
				t.Run("v"+version+"/"+nodeName, func(t *testing.T) {
					test := suite.NewControlPlaneTest(t, nodeName, version)

					// Feed in initial state and start the agent.
					test.
						UpdateObjectsFromFile(abs("init.yaml")).
						SetupEnvironment(modConfig).
						StartAgent().
						UpdateObjectsFromFile(abs("state1.yaml")).
						Eventually(func() error {
							time.Sleep(time.Second)
							f, _ := os.OpenFile("/tmp/dump.json", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
							if err := test.DB.WriteJSON(f); err != nil {
								return err
							}
							f.Close()
							return nil
						}).
						StopAgent()
				})
			}
		}
	})
}
