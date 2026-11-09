// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package reflectors

import (
	"github.com/cilium/cilium/pkg/metrics"
	"github.com/cilium/cilium/pkg/metrics/metric"
)

type serviceQuotaMetrics struct {
	Rejections metric.Counter
	Pending    metric.Gauge
	Ports      metric.Gauge
}

func newServiceQuotaMetrics() *serviceQuotaMetrics {
	return &serviceQuotaMetrics{
		Rejections: metric.NewCounter(metric.CounterOpts{Namespace: metrics.Namespace, Subsystem: "loadbalancer", Name: "service_quota_rejections_total", Help: "Service reconciliation attempts rejected by quota, including retries."}),
		Pending:    metric.NewGauge(metric.GaugeOpts{Namespace: metrics.Namespace, Subsystem: "loadbalancer", Name: "service_quota_pending", Help: "Kubernetes Services waiting for quota on this agent."}),
		Ports:      metric.NewGauge(metric.GaugeOpts{Namespace: metrics.Namespace, Subsystem: "loadbalancer", Name: "service_quota_ports", Help: "Service port units reserved by this agent across all namespaces."}),
	}
}
