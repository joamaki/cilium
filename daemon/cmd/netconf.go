package cmd

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/option"
	cnitypes "github.com/cilium/cilium/plugins/cilium-cni/types"
)

type netconfConfig struct {
	// ReadCNIConf points to the CNI configuration file.
	// This can be used to pass per node CNI configuration to Cilium.
	ReadCNIConf string

	// WriteCNIConfWhenReady writes the CNI configuration to the
	// specified location once the agent is ready to serve requests. This
	// allows to keep a Kubernetes node NotReady until Cilium is up and
	// running and able to schedule endpoints.
	WriteCNIConfWhenReady string
}

func (netconfConfig) Flags(flags *pflag.FlagSet) {
	flags.String(option.ReadCNIConfiguration, "", "Read the CNI configuration at specified path to extract per node configuration")
	flags.String(option.WriteCNIConfigurationWhenReady, "", fmt.Sprintf("Write the CNI configuration as specified via --%s to path when agent is ready", option.ReadCNIConfiguration))
}

// netconfCell implements reading and writing of the CNI configuration.
//
// The CNI configuration is written when the daemon has finished initialization.
// This signals to Kubernetes that the node is ready.
var netconfCell = cell.Module(
	"NetConf",
	cell.Config(netconfConfig{}),
	cell.Provide(readNetConf),
	cell.Invoke(writeNetConfHook),
)

func readNetConf(cfg netconfConfig) (*cnitypes.NetConf, error) {
	if cfg.ReadCNIConf != "" {
		return cnitypes.ReadNetConf(cfg.ReadCNIConf)
	}
	return nil, nil
}

func writeNetConfHook(cfg netconfConfig, netConf *cnitypes.NetConf, log logrus.FieldLogger, lc hive.Lifecycle) {
	lc.Append(hive.Hook{OnStart: func(context.Context) error {
		if cfg.WriteCNIConfWhenReady != "" {
			if cfg.ReadCNIConf == "" {
				log.Fatalf("%s must be set when using %s", option.ReadCNIConfiguration, option.WriteCNIConfigurationWhenReady)
			}
			if err := netConf.WriteFile(cfg.WriteCNIConfWhenReady); err != nil {
				return fmt.Errorf("unable to write CNI configuration file to %s: %s",
					cfg.WriteCNIConfWhenReady,
					err)
			} else {
				log.Infof("Wrote CNI configuration file to %s", cfg.WriteCNIConfWhenReady)
			}
		}
		return nil
	}})
}
