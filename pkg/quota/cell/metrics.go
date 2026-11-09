// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quotacell

import (
	"github.com/cilium/cilium/pkg/metrics"
	"github.com/cilium/cilium/pkg/metrics/metric"
)

type quotaMetrics struct {
	Excess metric.DeletableVec[metric.Gauge]
}

func newQuotaMetrics() *quotaMetrics {
	return &quotaMetrics{Excess: metric.NewGaugeVec(metric.GaugeOpts{
		Namespace: metrics.Namespace,
		Subsystem: "quota",
		Name:      "excess",
		Help:      "Units above the hard limit, labelled by namespace and resource. Only over-quota resources have a series.",
	}, []string{"namespace", "resource"})}
}
