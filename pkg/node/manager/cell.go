// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package manager

import (
	"context"

	"go.uber.org/fx"

	"github.com/spf13/pflag"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/option"
)

var Cell = hive.NewCell(
	"node-manager",
	fx.Provide(newAllNodeManager),
)

func newAllNodeManager(lc fx.Lifecycle) (*Manager, error) {
	cfg := NodeManagerConfig{option.Config}
	mngr, err := newManager("all", cfg)
	if err != nil {
		return nil, err
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			mngr.Start()
			return nil
		},
		OnStop: func(context.Context) error {
			mngr.Close()
			return nil
		},
	})

	return mngr, nil
}

// NodeManagerConfig contains the configuration options required by node manager.
type NodeManagerConfig struct {
	// TODO: split DaemonConfig up into BaseConfig/DatapathConfig etc. and
	// inject them.
	daemonConfig *option.DaemonConfig
}

func (NodeManagerConfig) CellFlags(flags *pflag.FlagSet) {
	// No flags specific to node manager. We're inheriting
	// the flags defined for DaemonConfig and allowing viper.Unmarshal
	// to fill them in. In future these should be provided via base and datapath
	// configuration structs.
}

func (cfg NodeManagerConfig) TunnelingEnabled() bool {
	return cfg.daemonConfig.Tunnel != option.TunnelDisabled
}
func (cfg NodeManagerConfig) RemoteNodeIdentitiesEnabled() bool {
	return cfg.daemonConfig.EnableRemoteNodeIdentity
}
func (cfg NodeManagerConfig) NodeEncryptionEnabled() bool {
	return cfg.daemonConfig.EncryptNode
}
func (cfg NodeManagerConfig) EncryptionEnabled() bool {
	return cfg.daemonConfig.EnableIPSec
}
