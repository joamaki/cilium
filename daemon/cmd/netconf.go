package cmd

import (
	"github.com/spf13/pflag"
	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/option"
	cnitypes "github.com/cilium/cilium/plugins/cilium-cni/types"
)

type netconfConfig struct {
	// ReadCNIConf points to the CNI configuration file.
	// This can be used to pass per node CNI configuration to Cilium.
	ReadCNIConf string
}

func (netconfConfig) CellFlags(flags *pflag.FlagSet) {
	flags.String(option.ReadCNIConfiguration, "", "Read the CNI configuration at specified path to extract per node configuration")

}

var netconfCell = hive.NewCellWithConfig[netconfConfig](
	"netconf",
	fx.Provide(readNetconf),
)

func readNetconf(cfg netconfConfig) (*cnitypes.NetConf, error) {
	if cfg.ReadCNIConf != "" {
		return cnitypes.ReadNetConf(cfg.ReadCNIConf)
	}
	return nil, nil
}
