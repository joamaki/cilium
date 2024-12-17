package main

import (
	"fmt"

	"github.com/cilium/cilium/pkg/metrics"
	"github.com/cilium/hive/cell"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
)

// DValue is a diagnostic value. ALl diagnostic values are
// CEL values.
type DValue interface {
	ref.Val
}

type (
	DString = types.String
	DInt    = types.Int
	DFloat  = types.Double
	DBool   = types.Bool
	DMap    = traits.Mapper
	DList   = traits.Lister
)

// DAnnoFloat is an annotated float. The annotation is only used outside CEL to show
// additional information when evaluation fails. E.g. this could be used to show for
// example when a specific metric value last changed.
type DAnnoFloat struct {
	types.Double
	anno string
}

func (daf DAnnoFloat) String() string {
	return fmt.Sprintf("%g (%s)", daf.Double, daf.anno)
}

var _ ref.Val = DAnnoFloat{}
var _ traits.Comparer = DAnnoFloat{}

var dreg, _ = types.NewRegistry()

func NewStringMap(m map[string]string) DMap {
	return types.NewStringStringMap(dreg, m)
}
func NewFloatMap(m map[string]float64) DMap {
	mRef := map[ref.Val]ref.Val{}
	for k, v := range m {
		mRef[DString(k)] = DFloat(v)
	}
	return types.NewRefValMap(dreg, mRef)
}

func NewIntMap(m map[string]int) DMap {
	return types.NewDynamicMap(dreg, m)
}

type DiagnosticCollector interface {
	CollectDiagnostics() map[string]DValue
}

type exampleCollector struct{}

// CollectDiagnostics implements DiagnosticCollector.
func (e *exampleCollector) CollectDiagnostics() map[string]DValue {
	return map[string]DValue{
		"foo":     DString("bar"),
		"bar":     DAnnoFloat{1234, "extra info here"},
		"baz":     NewFloatMap(map[string]float64{"a": 1.0, "b": 2.0}),
		"quux":    NewStringMap(map[string]string{"a": "aa"}),
		"enabled": DBool(true),
	}
}

func newExampleCollector() *exampleCollector {
	return &exampleCollector{}
}

var _ DiagnosticCollector = &exampleCollector{}

type CollectorOut struct {
	cell.Out

	Collector DiagnosticCollector `group:"diagnostic-collectors"`
}

type CollectorsIn struct {
	cell.In

	Collectors []DiagnosticCollector `group:"diagnostic-collectors"`
}

type qualifiedCollector struct {
	moduleID cell.FullModuleID
	coll     DiagnosticCollector
}

func (q qualifiedCollector) CollectDiagnostics() map[string]DValue {
	m := map[string]DValue{}
	for k, v := range q.coll.CollectDiagnostics() {
		m[q.moduleID.String()+"."+k] = v
	}
	return m
}

func Diagnostics[T DiagnosticCollector]() cell.Cell {
	return cell.Provide(
		func(x T, mid cell.FullModuleID) CollectorOut {
			return CollectorOut{
				Collector: qualifiedCollector{
					moduleID: mid,
					coll:     x,
				},
			}
		})
}

func collapse(in CollectorsIn) map[string]ref.Val {
	m := map[string]ref.Val{}
	for _, dc := range in.Collectors {
		for k, v := range dc.CollectDiagnostics() {
			m[k] = v
		}
	}
	return m
}

type metricsCollector struct {
	reg *metrics.Registry
}

func newMetricsCollector(r *metrics.Registry) *metricsCollector {
	return &metricsCollector{r}
}

func (m *metricsCollector) CollectDiagnostics() map[string]DValue {
	out := map[string]DValue{}
	for k, v := range m.reg.Diagnostics() {
		out[k] = DFloat(v)
	}
	return out
}
