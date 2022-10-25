// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package manager

import (
	"context"

	"github.com/cilium/cilium/pkg/backoff"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/option"
)

var Cell = cell.Module(
	"NodeManager",
	cell.Provide(newAllNodeManager),
)

func newAllNodeManager(lc hive.Lifecycle, clusterSizeBackoff *backoff.ClusterSizeBackoff) (*Manager, error) {
	mngr, err := NewManager("all", clusterSizeBackoff, option.Config)
	if err != nil {
		return nil, err
	}

	lc.Append(hive.Hook{
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
