// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package iptables

import (
	"go.uber.org/fx"
)

var Module = fx.Module(
	"iptables-manager",
	fx.Provide(newIptablesManager),
)

func newIptablesManager(lc fx.Lifecycle) *IptablesManager {
	mgr := &IptablesManager{}
	mgr.Init()
	return mgr
}
