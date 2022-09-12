package iptables

import (
	"context"

	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/datapath"
	"github.com/cilium/cilium/pkg/hive"
)

var Cell = hive.NewCell(
	"iptables",
	fx.Provide(
		newIptablesManager,
	),
)

func newIptablesManager(lc fx.Lifecycle) datapath.IptablesManager {
	m := &iptablesManager{}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			m.Init()
			return nil
		},
	})
	return m
}
