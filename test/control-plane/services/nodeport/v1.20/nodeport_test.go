// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package v1_20

import (
	"fmt"
	"testing"

	fakeDatapath "github.com/cilium/cilium/pkg/datapath/fake"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/test/control-plane/services"
)

func TestNodePort(t *testing.T) {
	modConfig := func(c *option.DaemonConfig) { c.EnableNodePort = true }
	test := services.NewGoldenServicesTest(t, "nodeport-control-plane")

	test.Steps[0].AddValidationFunc(func(dp *fakeDatapath.FakeDatapath) error {
		fmt.Printf("identity changes: %#v\n", dp.FakeIPCacheListener())

		for i, change := range dp.FakeIPCacheListener().GetIdentityChanges() {
			fmt.Printf("\t%d: [%s] meta=%v cidr=%s newID=%s\n", i, change.ModType, change.Meta, change.CIDR, change.NewID.ID)
		}
		return fmt.Errorf("blah")
	})

	test.Run(t, "1.20", modConfig)
}
