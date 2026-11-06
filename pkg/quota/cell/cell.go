// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quotacell

import (
	"github.com/cilium/cilium/pkg/metrics"
	"github.com/cilium/cilium/pkg/quota"
	quotasources "github.com/cilium/cilium/pkg/quota/sources"
	"github.com/cilium/hive/cell"
	"github.com/cilium/statedb"
)

// Cell provides support for resource quotas.
var Cell = cell.Module(
	"quota",
	"Resource quotas",
	cell.Config(quotasources.DefaultConfig),
	metrics.Metric(newQuotaMetrics),
	cell.ProvidePrivate(quotasources.NewTableAndReflector),
	cell.Provide(statedb.RWTable[quota.ResourceQuota].ToTable, quota.NewAccounting),
	cell.Invoke(quotasources.ApplyConfig, observeHealth),
)
