// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package metric

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

type WithMetadata interface {
	IsEnabled() bool
	SetEnabled(bool)
	Opts() Opts
	Labels() LabelDescriptions
}

type metric struct {
	opts    Opts
	enabled bool
}

func (b *metric) IsEnabled() bool {
	return b.enabled
}

func (b *metric) SetEnabled(e bool) {
	b.enabled = e
}

func (b *metric) Opts() Opts {
	return Opts(b.opts)
}

func (b *metric) Labels() LabelDescriptions {
	var labels LabelDescriptions
	for constLabel, value := range b.opts.ConstLabels {
		labels = append(labels, LabelDescription{
			Name:        constLabel.Name,
			Description: constLabel.Description,
			KnownValues: []KnownValue{
				{
					Name: value,
				},
			},
		})
	}
	return labels
}

type MetricType string

// TODO to be expanded
const (
	MetricTypeEndpoint MetricType = "endpoint"
)

type Opts struct {
	// Namespace, Subsystem, and Name are components of the fully-qualified
	// name of the Metric (created by joining these components with
	// "_"). Only Name is mandatory, the others merely help structuring the
	// name. Note that the fully-qualified name of the metric must be a
	// valid Prometheus metric name. TODO: can we hardcode namespace to "cilium"
	Namespace string
	Subsystem Subsystem
	Name      string

	// Help provides information about this metric.
	//
	// Metrics with the same fully-qualified name must have the same Help
	// string.
	//
	// This string is included with the data in the prometheus endpoint and should be brief.
	Help string

	// Description is a more verbose description of the metric for documentation purposes.
	Description string

	// If true, the metrics are enabled unless specifically requested to be disabled
	EnabledByDefault bool

	// The type/category of metric. TODO can this replace the subsystem?
	MetricType MetricType

	// ConstLabels are used to attach fixed labels to this metric. Metrics
	// with the same fully-qualified name must have the same label names in
	// their ConstLabels.
	//
	// ConstLabels are only used rarely. In particular, do not use them to
	// attach the same labels to all your metrics. Those use cases are
	// better covered by target labels set by the scraping Prometheus
	// server, or by one specific metric (e.g. a build_info or a
	// machine_role metric). See also
	// https://prometheus.io/docs/instrumenting/writing_exporters/#target-labels-not-static-scraped-labels
	ConstLabels ConstLabels
}

func (o Opts) FQDNName() string {
	var parts []string
	if o.Namespace != "" {
		parts = append(parts, o.Namespace)
	}
	if o.Subsystem.Name != "" {
		parts = append(parts, o.Subsystem.Name)
	}
	parts = append(parts, o.Name)

	return strings.Join(parts, "_")
}

type LabelDescriptions []LabelDescription

type LabelDescription struct {
	Name        string
	Description string
	KnownValues []KnownValue
}

type KnownValue struct {
	Name        string
	Description string
}

func (l LabelDescriptions) labelNames() []string {
	names := make([]string, len(l))
	for i, desc := range l {
		names[i] = desc.Name
	}
	return names
}

type ConstLabels map[ConstLabel]string

type ConstLabel struct {
	Name        string
	Description string
}

type Subsystem struct {
	Name        string
	DocName     string
	Description string
}

func (l ConstLabels) toPrometheus() prometheus.Labels {
	labels := make(prometheus.Labels, len(l))
	for label, value := range l {
		labels[label.Name] = value
	}
	return labels
}

type Vec[T any] interface {
	WithMetadata

	CurryWith(labels prometheus.Labels) (Vec[T], error)
	GetMetricWith(labels prometheus.Labels) (T, error)
	GetMetricWithLabelValues(lvs ...string) (T, error)
	With(labels prometheus.Labels) T
	WithLabelValues(lvs ...string) T
	LabelDescriptions() LabelDescriptions
}

type DeletableVec[T any] interface {
	Vec[T]
	Delete(labels prometheus.Labels) bool
	DeleteLabelValues(lvs ...string) bool
	DeletePartialMatch(labels prometheus.Labels) int
	Reset()
}
