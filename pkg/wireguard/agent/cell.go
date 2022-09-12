package agent

import (
	"context"
	"fmt"
	"path/filepath"

	"go.uber.org/fx"

	"github.com/sirupsen/logrus"

	"github.com/cilium/cilium/pkg/datapath"
	"github.com/cilium/cilium/pkg/datapath/link"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/wireguard/types"
)

var Cell = hive.NewCell(
	"wireguard",

	fx.Provide(newAgent),
)

func newAgent(lc fx.Lifecycle, log logrus.FieldLogger) (*Agent, datapath.WireguardAgent, error) {
	var wgAgent *Agent
	if option.Config.EnableWireguard {
		switch {
		case option.Config.EnableIPSec:
			return nil, nil, fmt.Errorf("Wireguard (--%s) cannot be used with IPSec (--%s)",
				option.EnableWireguard, option.EnableIPSecName)
		case option.Config.EnableL7Proxy:
			return nil, nil, fmt.Errorf("Wireguard (--%s) is not compatible with L7 proxy (--%s)",
				option.EnableWireguard, option.EnableL7Proxy)
		}

		var err error
		privateKeyPath := filepath.Join(option.Config.StateDir, types.PrivKeyFilename)
		wgAgent, err = NewAgent(privateKeyPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialize wireguard: %s", err)
		}

		lc.Append(fx.Hook{
			OnStop: func(context.Context) error {
				return wgAgent.Close()
			},
		})
	} else {
		// Delete wireguard device from previous run (if such exists)
		link.DeleteByName(types.IfaceName)
	}
	return wgAgent, wgAgent, nil
}
