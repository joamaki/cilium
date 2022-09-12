// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package linux

import (
	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/datapath"
	"github.com/cilium/cilium/pkg/datapath/loader"
	"github.com/cilium/cilium/pkg/hive"
)

var DatapathCell = hive.NewCell(
	"linux-datapath",

	fx.Provide(
		NewDatapath,
		newLoader,
	),
)

func newLoader() datapath.Loader {
	return loader.NewLoader(canDisableDwarfRelocations)
}
