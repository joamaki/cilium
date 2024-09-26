package metrics

import (
	"flag"
	"fmt"
	"os"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/rogpeppe/go-internal/testscript"
)

const TestScriptRegistryKey = "metrics-registry"

func SetupTestScript(e *testscript.Env, reg *Registry) {
	e.Values[TestScriptRegistryKey] = reg
}

func DumpMetricsCmd(ts *testscript.TestScript, neg bool, args []string) {
	var flags flag.FlagSet
	prefix := flags.String("prefix", "", "Prefix to match the metric name on")
	if err := flags.Parse(args); err != nil {
		ts.Fatalf("bad args: %s", err)
	}
	args = flags.Args()
	if len(args) != 1 {
		ts.Fatalf("usage: metrics (-prefix=<prefix>) <filename>")
	}

	reg := ts.Value(TestScriptRegistryKey).(*Registry)
	if reg == nil {
		ts.Fatalf("no registry provided with key %q", TestScriptRegistryKey)
	}

	currentMetrics, err := reg.inner.Gather()
	if err != nil {
		ts.Fatalf("Gather: %s", err)
	}
	f, err := os.OpenFile(ts.MkAbs(args[0]), os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		ts.Fatalf("OpenFile(%s): %s", args[0], err)
	}
	defer f.Close()
	for _, val := range currentMetrics {
		metricName := val.GetName()
		metricType := val.GetType()

		if *prefix != "" && !strings.HasPrefix(metricName, *prefix) {
			continue
		}

		for _, metricLabel := range val.Metric {
			labelPairs := metricLabel.GetLabel()
			labels := make([]string, 0, len(labelPairs))

			for _, label := range metricLabel.GetLabel() {
				labels = append(labels, label.GetName()+"="+label.GetValue())
			}

			var value float64
			switch metricType {
			case dto.MetricType_COUNTER:
				value = metricLabel.Counter.GetValue()
			case dto.MetricType_GAUGE:
				value = metricLabel.GetGauge().GetValue()
			case dto.MetricType_UNTYPED:
				value = metricLabel.GetUntyped().GetValue()
			case dto.MetricType_SUMMARY:
				value = metricLabel.GetSummary().GetSampleSum()
			case dto.MetricType_HISTOGRAM:
				value = metricLabel.GetHistogram().GetSampleSum()
			default:
				continue
			}

			fmt.Fprintf(f, "%s{%s}: %v\n", metricName, strings.Join(labels, " "), value)
		}
	}
}
