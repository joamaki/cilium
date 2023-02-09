// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package metrics

import "github.com/cilium/cilium/pkg/hive/cell"

var Cell = cell.Module(
	"metrics",
	"Metrics",

	cell.Config(defaultRegistryConfig),
	cell.Provide(NewRegistry),
	//cell.Provide(NewLoggingHook),
	cell.Invoke(func(*Registry) {}),
)
