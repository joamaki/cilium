// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

import "github.com/cilium/cilium/pkg/metrics"

func Metric[S any](ctor func() S) Cell {
	return metrics.Metric[S](ctor)
}
