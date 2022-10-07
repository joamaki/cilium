// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

// PLACEHOLDERS. These would be in pkg/metrics.
type Metric struct {
	Meta MetricMeta
	// some prometheus thing here
}
type MetricMeta struct {
	Enabled     bool
	Description string
}

type metricsOut struct {
	Out
	Metrics []*Metric `group:"metrics,flatten"`
}

func Metrics[Cfg Flagger](ctor func(Cfg) []*Metric) Cell {
	// Wrap the constructor to provide the metrics in a
	// value group.
	return Provide(func(cfg Cfg) (out metricsOut) {
		out.Metrics = ctor(cfg)
		return
	})
}
