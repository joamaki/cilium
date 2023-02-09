package datapath

import (
	"github.com/cilium/cilium/lbtest/datapath/devices"
	"github.com/cilium/cilium/lbtest/datapath/loadbalancer"
	"github.com/cilium/cilium/lbtest/datapath/loader"
	"github.com/cilium/cilium/lbtest/datapath/monitor"
	"github.com/cilium/cilium/pkg/datapath/linux/maps/lbmap"
	datapathTypes "github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/testutils/mockmaps"
	"github.com/spf13/pflag"
)

type DatapathConfig struct {
	EnableIPv4 bool
	EnableIPv6 bool
}

func (def DatapathConfig) Flags(flags *pflag.FlagSet) {
	flags.Bool("enable-ipv4", def.EnableIPv4, "Enable IPv4 support")
	flags.Bool("enable-ipv6", def.EnableIPv6, "Enable IPv6 support")
}

var defaultDatapathConfig = DatapathConfig{
	EnableIPv4: true,
	EnableIPv6: true,
}

func lbmapInitParams(cfg DatapathConfig) lbmap.InitParams {
	return lbmap.InitParams{
		IPv4:                     cfg.EnableIPv4,
		IPv6:                     cfg.EnableIPv6,
		MaxSockRevNatMapEntries:  65536,
		ServiceMapMaxEntries:     65536,
		BackEndMapMaxEntries:     65536,
		RevNatMapMaxEntries:      65536,
		AffinityMapMaxEntries:    65536,
		SourceRangeMapMaxEntries: 65536,
		MaglevMapMaxEntries:      65536,
	}
}

var Cell = cell.Module(
	"lbtest-datapath",
	"Datapath for lbtest",

	cell.Config(defaultDatapathConfig),

	cell.Provide(lbmapInitParams),
	lbmap.Cell,

	loadbalancer.Cell,
	devices.Cell,
	monitor.Cell,
	loader.Cell,
)

var fakeLBMapCell = cell.Module(
	"fake-lbmap",
	"Fake LBMap",
	cell.Provide(
		func() datapathTypes.LBMap { return mockmaps.NewLBMockMap() },
	),
)
