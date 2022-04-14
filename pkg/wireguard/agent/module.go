// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package agent

import (
	"context"
	"fmt"
	"path/filepath"

	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/datapath"
	"github.com/cilium/cilium/pkg/datapath/link"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/wireguard/types"
)

var (
	Module = fx.Module(
		"wireguard",
		fx.Provide(newWireguardAgent),
	)
)

func newWireguardAgent(lc fx.Lifecycle) (datapath.WireguardAgent, error) {
	if !option.Config.EnableWireguard {
		// Delete wireguard device from previous run (if such exists)
		link.DeleteByName(types.IfaceName)
		return nil, nil
	}

	switch {
	case option.Config.EnableIPSec:
		return nil, fmt.Errorf("Wireguard (--%s) cannot be used with IPSec (--%s)",
			option.EnableWireguard, option.EnableIPSecName)
	case option.Config.EnableL7Proxy:
		return nil, fmt.Errorf("Wireguard (--%s) is not compatible with L7 proxy (--%s)",
			option.EnableWireguard, option.EnableL7Proxy)
	}

	var err error
	privateKeyPath := filepath.Join(option.Config.StateDir, types.PrivKeyFilename)
	wgAgent, err := NewAgent(privateKeyPath)
	if err != nil {
		log.WithError(err).Fatal("Failed to initialize wireguard")
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			return wgAgent.Close()
		},
	})
	return wgAgent, nil
}
