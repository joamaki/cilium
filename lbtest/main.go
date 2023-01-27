package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/logging"
)

var LBTest = cell.Module(
	"lbtest",
	"Load-balancer test",

	client.Cell,
	k8s.SharedResourcesCell,

	ControlPlane,
	Datapath,

	selftestCell,

	//cell.Config(rootConfig{}),
	cell.Invoke(setupLogger),
)

type rootConfig struct {
	Debug bool
}

func (rootConfig) Flags(flags *pflag.FlagSet) {
	flags.Bool("debug", false, "Enable debug output")
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
			h.PrintObjects()
			fmt.Println("--------------------------------------------")

			if err := h.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Fatal:\n%s\n", err.Error())
				os.Exit(1)
			}
		},
	}
	h.RegisterFlags(cmd.Flags())
	cmd.Execute()
}
