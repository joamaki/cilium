package cmd

import (
	"fmt"

	"github.com/spf13/pflag"
	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/promise"
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

func (netconfConfig) CellFlags(flags *pflag.FlagSet) {
	flags.String(option.ReadCNIConfiguration, "", "Read the CNI configuration at specified path to extract per node configuration")
	flags.String(option.WriteCNIConfigurationWhenReady, "", fmt.Sprintf("Write the CNI configuration as specified via --%s to path when agent is ready", option.ReadCNIConfiguration))
}

var netconfCell = hive.NewCellWithConfig[netconfConfig](
	"netconf",
	fx.Provide(readNetconf),
)

var netconfWriterCell = hive.NewCell(
	"netconf-writer",
	fx.Invoke(writeNetconf),
)

func readNetconf(cfg netconfConfig) (*cnitypes.NetConf, error) {
	if cfg.ReadCNIConf != "" {
		return cnitypes.ReadNetConf(cfg.ReadCNIConf)
	}
	return nil, nil
}

func writeNetconf(cfg netconfConfig, netConf *cnitypes.NetConf, shutdowner fx.Shutdowner, dp promise.Promise[*Daemon]) {
	go func() {
		// Wait for daemon initialization to finish before writing.
		dp.Await()

		if cfg.WriteCNIConfWhenReady != "" {
			if cfg.ReadCNIConf == "" {
				log.Fatalf("%s must be set when using %s", option.ReadCNIConfiguration, option.WriteCNIConfigurationWhenReady)
			}
			if err := netConf.WriteFile(cfg.WriteCNIConfWhenReady); err != nil {
				log.Errorf("unable to write CNI configuration file to %s: %s",
					cfg.WriteCNIConfWhenReady,
					err)
				shutdowner.Shutdown()
			} else {
				log.Infof("Wrote CNI configuration file to %s", cfg.WriteCNIConfWhenReady)
			}
		}

	}()
}
