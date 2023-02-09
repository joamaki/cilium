package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/cilium/cilium/lbtest/controlplane"
	"github.com/cilium/cilium/lbtest/datapath"
	"github.com/cilium/cilium/lbtest/datapath/monitor"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/metrics"
)

var LBTest = cell.Module(
	"lbtest",
	"Load-balancer test",

	client.Cell,
	k8s.SharedResourcesCell,
	metrics.Cell,

	apiserverCell,

	controlplane.Cell,
	datapath.Cell,

	selftestCell,

	cell.Config(rootConfig{}),
	cell.Provide(getDerivedConfigs),
	cell.Invoke(setupLogger),
)

type rootConfig struct {
	Debug bool
}

func (rootConfig) Flags(flags *pflag.FlagSet) {
	flags.Bool("debug", false, "Enable debug output")
}

// derivedConfigs are the module configurations that are derived and not
// directly configured via flags.
type derivedConfigs struct {
	cell.Out

	Monitor monitor.MonitorConfig
}

func getDerivedConfigs(cfg rootConfig) derivedConfigs {
	return derivedConfigs{
		Monitor: monitor.MonitorConfig{EnableMonitor: cfg.Debug},
	}
}

func setupLogger(cfg rootConfig) {
	if cfg.Debug {
		logging.SetLogLevelToDebug()
	}
}

func main() {
	h := hive.New(LBTest)

	cmd := &cobra.Command{
		Use: "lbtest",
		Run: func(*cobra.Command, []string) {
			if err := h.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Fatal:\n%s\n", err.Error())
				os.Exit(1)
			}
		},
	}
	h.RegisterFlags(cmd.Flags())
	cmd.AddCommand(h.Command())
	cmd.Execute()
}
