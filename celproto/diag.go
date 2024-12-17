package main

import (
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
		"bar":     DInt(1234),
		"baz":     NewFloatMap(map[string]float64{"a": 1.0, "b": 2.0}),
		"quux":    NewStringMap(map[string]string{"a": "aa"}),
		"enabled": DBool(true),
	}
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

func collapse(in CollectorsIn) map[string]ref.Val {
	m := map[string]ref.Val{}
	for _, dc := range in.Collectors {
		for k, v := range dc.CollectDiagnostics() {
			m[k] = v
		}
	}
	return m
}
