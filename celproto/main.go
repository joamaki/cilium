package main

import (
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

const (
	test1 = `
		foo: foo != 'bar'   # Foo should be bar!
		bar: bar > 1000     # High bar value, check quux
		quux: quux['a'] == 'aa' # aaa
		baz: baz['a'] > 0.1 # a is fubar'd!
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
	var e exampleCollector
	data := collapse(CollectorsIn{Collectors: []DiagnosticCollector{&e}})

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
