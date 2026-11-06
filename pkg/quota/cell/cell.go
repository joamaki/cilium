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
	cell.ProvidePrivate(quota.NewTable),
	cell.Provide(
		statedb.RWTable[quota.ResourceQuota].ToTable,
		quota.NewAccounting,
	),
)
