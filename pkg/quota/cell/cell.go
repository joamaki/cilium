// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quotacell

import (
	"github.com/cilium/hive/cell"
	"github.com/cilium/statedb"

	"github.com/cilium/cilium/pkg/quota"
)

// Cell provides support for resource quotas.
var Cell = cell.Module(
	"quota",
	"Resource quotas",
	cell.Provide(newTable),
)

func newTable(db *statedb.DB) (statedb.Table[quota.ResourceQuota], error) {
	table, err := quota.NewTable(db)
	if err != nil {
		return nil, err
	}
	return table.ToTable(), nil
}
