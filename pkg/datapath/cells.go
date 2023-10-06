// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package datapath

import (
	"log"
	"path/filepath"

	"github.com/cilium/cilium/pkg/bpf"
	"github.com/cilium/cilium/pkg/datapath/agentliveness"
	"github.com/cilium/cilium/pkg/datapath/garp"
	"github.com/cilium/cilium/pkg/datapath/iptables"
	"github.com/cilium/cilium/pkg/datapath/l2responder"
	"github.com/cilium/cilium/pkg/datapath/link"
	linuxdatapath "github.com/cilium/cilium/pkg/datapath/linux"
	dpcfg "github.com/cilium/cilium/pkg/datapath/linux/config"
	"github.com/cilium/cilium/pkg/datapath/linux/utime"
	"github.com/cilium/cilium/pkg/datapath/loader"
	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/maps"
	"github.com/cilium/cilium/pkg/maps/eventsmap"
	"github.com/cilium/cilium/pkg/maps/nodemap"
	monitorAgent "github.com/cilium/cilium/pkg/monitor/agent"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/option"
	wg "github.com/cilium/cilium/pkg/wireguard/agent"
	wgTypes "github.com/cilium/cilium/pkg/wireguard/types"
)

// Datapath provides the privileged operations to apply control-plane
// decision to the kernel.
var Cell = cell.Module(
	"datapath",
	"Datapath",

	cell.Group(
		// FeatureFlags collects the set of datapath features required by control-plane
		// modules that were provided as 'types.FeatureFlagOption'.
		cell.Provide(types.NewFeatureFlags),

		// featureFlagsFromDaemonConfig populates feature flags from option.Config.
		cell.Provide(featureFlagsFromDaemonConfig),
	),

	// Provides all BPF Map which are already provided by via hive cell.
	maps.Cell,

	// Utime synchronizes utime from userspace to datapath via configmap.Map.
	utime.Cell,

	// The cilium events map, used by the monitor agent.
	eventsmap.Cell,

	// The monitor agent, which multicasts cilium and agent events to its subscribers.
	monitorAgent.Cell,

	cell.Provide(
		newWireguardAgent,
		newDatapath,
		loader.NewLoader,
	),

	// This cell periodically updates the agent liveness value in configmap.Map to inform
	// the datapath of the liveness of the agent.
	agentliveness.Cell,

	// The responder reconciler takes desired state about L3->L2 address translation responses and reconciles
	// it to the BPF L2 responder map.
	l2responder.Cell,

	// This cell defines StateDB tables and their schemas for tables which are used to transfer information
	// between datapath components and more high-level components.
	tables.Cell,

	// Gratuitous ARP event processor emits GARP packets on k8s pod creation events.
	garp.Cell,

	// This cell provides the object used to write the headers for datapath program types.
	dpcfg.Cell,

	cell.Provide(func(dp types.Datapath) types.NodeIDHandler {
		return dp.NodeIDs()
	}),

	// DevicesController manages the devices and routes tables
	linuxdatapath.DevicesControllerCell,
	cell.Provide(func(cfg *option.DaemonConfig) linuxdatapath.DevicesConfig {
		// Provide the configured devices to the devices controller.
		// This is temporary until DevicesController takes ownership of the
		// device-related configuration options.
		return linuxdatapath.DevicesConfig{Devices: cfg.GetDevices()}
	}),
)

func newWireguardAgent(lc hive.Lifecycle, localNodeStore *node.LocalNodeStore) *wg.Agent {
	var wgAgent *wg.Agent
	if option.Config.EnableWireguard {
		if option.Config.EnableIPSec {
			log.Fatalf("WireGuard (--%s) cannot be used with IPsec (--%s)",
				option.EnableWireguard, option.EnableIPSecName)
		}

		var err error
		privateKeyPath := filepath.Join(option.Config.StateDir, wgTypes.PrivKeyFilename)
		wgAgent, err = wg.NewAgent(privateKeyPath, localNodeStore)
		if err != nil {
			log.Fatalf("failed to initialize WireGuard: %s", err)
		}

		lc.Append(hive.Hook{
			OnStop: func(hive.HookContext) error {
				wgAgent.Close()
				return nil
			},
		})
	} else {
		// Delete WireGuard device from previous run (if such exists)
		link.DeleteByName(wgTypes.IfaceName)
	}
	return wgAgent
}

func newDatapath(params datapathParams) types.Datapath {
	datapathConfig := linuxdatapath.DatapathConfiguration{
		HostDevice: defaults.HostDevice,
		ProcFs:     option.Config.ProcFs,
	}

	iptablesManager := &iptables.IptablesManager{}

	params.LC.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			// FIXME enableIPForwarding should not live here
			if err := enableIPForwarding(); err != nil {
				log.Fatalf("enabling IP forwarding via sysctl failed: %s", err)
			}

			iptablesManager.Init()
			return nil
		}})

	datapath := linuxdatapath.NewDatapath(datapathConfig, iptablesManager, params.WgAgent, params.NodeMap, params.ConfigWriter, params.Loader)

	params.LC.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			datapath.NodeIDs().RestoreNodeIDs()
			return nil
		},
	})

	return datapath
}

type datapathParams struct {
	cell.In

	LC      hive.Lifecycle
	WgAgent *wg.Agent

	// Force map initialisation before loader. You should not use these otherwise.
	// Some of the entries in this slice may be nil.
	BpfMaps []bpf.BpfMap `group:"bpf-maps"`

	NodeMap nodemap.Map

	Loader *loader.Loader

	// Depend on DeviceManager to ensure devices have been resolved.
	// This is required until option.Config.GetDevices() has been removed and
	// uses of it converted to Table[Device].
	DeviceManager *linuxdatapath.DeviceManager

	ConfigWriter types.ConfigWriter
}

func featureFlagsFromDaemonConfig(cfg *option.DaemonConfig) types.FeatureFlagsOut {
	return types.FeatureFlagsOut{
		Option: func(flags *types.FeatureFlags) {
			flags.Tunneling.Raise(
				// We check if routing mode is not native rather than checking if it's
				// tunneling because, in unit tests, RoutingMode is usually not set and we
				// would like for TunnelingEnabled to default to the actual default
				// (tunneling is enabled) in that case.
				cfg.RoutingMode != option.RoutingModeNative,
			)
			flags.EgressGatewayCommon.Raise(cfg.EnableIPv4EgressGateway)
		},
	}

}
