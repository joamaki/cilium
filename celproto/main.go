package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/metrics"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/hive/cell"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

const (
	test1 = `
		foo: agent.example.foo != 'bar'   # Foo should be bar!
		bar: agent.example.bar > 1000     # High bar value, check quux
		quux: agent.example.quux['a'] == 'aa' # aaa
		baz: agent.example.baz['a'] > 0.1 # a is fubar'd!
		double: agent.example.bar > 1000 && agent.example.baz['a'] > 0.1 # double trouble

		goroutines: metrics.go_goroutines > 2.0 # High number of goroutines
		heapuse: metrics.go_memstats_heap_inuse_bytes > 4e+06 # High heap usage
		slowpolicy: metrics.cilium_policy_implementation_delay.p90 > 0.1 # Slow policy implementation

		
	`
)

type diagExpr struct {
	name    string
	expr    string
	comment string
}

func parseDiagnosticExpressions(txt string) (out []diagExpr) {
	lines := strings.Split(txt, "\n")
	for _, line := range lines {
		line = strings.Trim(line, " \t")
		if len(line) == 0 {
			continue
		}
		name, expr, found := strings.Cut(line, ":")
		if !found {
			panic("bad input: " + line)
		}
		expr, comment, _ := strings.Cut(expr, "#")
		expr = strings.Trim(expr, " \t")
		comment = strings.Trim(comment, " \t")
		out = append(out, diagExpr{name, expr, comment})
	}
	return
}

func main() {
	var collectors CollectorsIn

	h := hive.New(
		cell.Module("metrics", "Metrics",
			cell.Provide(newMetricsCollector),
			Diagnostics[*metricsCollector](),
		),

		cell.Module("agent", "Agent",
						
			cell.Module("example", "Example",
				cell.Provide(newExampleCollector),
				Diagnostics[*exampleCollector](),
			),
		),

		cell.Invoke(
			func(in CollectorsIn) {
				collectors = in
			},
		),

		cell.Provide(func() *option.DaemonConfig {
			return &option.DaemonConfig{}
		}),
		metrics.Cell,
	)
	h.Start(slog.Default(), context.TODO())

	data := collapse(collectors)
	fmt.Printf("Collected diagnostics data:\n")
	for k, v := range data {
		fmt.Printf("  %s: %v\n", k, v)
	}
	fmt.Println()

	exprs := parseDiagnosticExpressions(test1)
	for _, expr := range exprs {
		failed, det := run(expr.expr, data)
		if failed {
			fmt.Printf("%q: %s\n", expr.expr, expr.comment)
			for k, v := range det {
				fmt.Printf("  - %s: '%v'\n", k, v)
			}
			fmt.Println()
		}
	}
}

func run(txt string, data map[string]ref.Val) (bool, map[string]any) {
	env, err := cel.NewEnv()
	if err != nil {
		panic(err)
	}
	for k, v := range data {
		typ := v.Type().(*types.Type)
		if typ.Kind() == types.MapKind {
			typ = cel.MapType(types.StringType, types.DynType)
		}
		env, _ = env.Extend(cel.Constant(k, typ, v))
	}

	ast, iss := env.Parse(txt)
	if err := iss.Err(); err != nil {
		panic(err)
	}

	checked, iss := env.Check(ast)
	if err := iss.Err(); err != nil {
		panic(err)
	}

	prog, err := env.Program(checked, cel.EvalOptions(cel.OptTrackState))
	if err != nil {
		panic(err)
	}

	out, _, err := prog.Eval(cel.NoVars())
	if err != nil {
		panic(err)
	}

	// Collect all constant references from the expression.
	refs := map[string]any{}
	for _, info := range checked.NativeRep().ReferenceMap() {
		if info.Name != "" {
			refs[info.Name] = info.Value
		}
	}

	switch out := out.(type) {
	case types.Bool:
		return bool(out), refs
	default:
		panic("bad type")
	}
}
