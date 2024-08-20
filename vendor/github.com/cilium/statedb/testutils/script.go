// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package testutils

import (
	"bytes"
	"flag"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cilium/statedb"
	"github.com/rogpeppe/go-internal/testscript"
	"gopkg.in/yaml.v3"
)

type Cmd = func(ts *testscript.TestScript, neg bool, args []string)

func ShowTableCmd[Obj statedb.TableWritable](db *statedb.DB, tbl statedb.Table[Obj]) Cmd {
	var zero Obj
	return func(ts *testscript.TestScript, neg bool, args []string) {
		var buf bytes.Buffer
		w := tabwriter.NewWriter(&buf, 5, 4, 3, ' ', 0)
		fmt.Fprintf(w, "%s\n", strings.Join(zero.TableHeader(), "\t"))
		for obj := range tbl.All(db.ReadTxn()) {
			fmt.Fprintf(w, "%s\n", strings.Join(obj.TableRow(), "\t"))
		}
		w.Flush()
		ts.Logf("%s", buf.String())
	}
}

func CompareTableCmd[Obj statedb.TableWritable](db *statedb.DB, tbl statedb.Table[Obj]) Cmd {
	var zero Obj
	return func(ts *testscript.TestScript, neg bool, args []string) {
		var flags flag.FlagSet
		wait := flags.Duration("wait", 0, "Wait for the table contents to match")
		err := flags.Parse(args)
		args = args[len(args)-flags.NArg():]
		if err != nil || len(args) != 1 {
			ts.Fatalf("usage: cmp [-wait=<duration>] file")
		}

		data := ts.ReadFile(args[0])
		lines := strings.Split(data, "\n")
		if len(lines) < 1 {
			ts.Fatalf("input too short")
		}
		lines = slices.DeleteFunc(lines, func(line string) bool {
			return strings.TrimSpace(line) == ""
		})

		header := zero.TableHeader()
		columnNames, columnPositions := splitHeaderLine(lines[0])
		columnIndexes := make([]int, 0, len(header))
	loop:
		for _, name := range columnNames {
			for i, name2 := range header {
				if strings.EqualFold(name, name2) {
					columnIndexes = append(columnIndexes, i)
					continue loop
				}
			}
			ts.Fatalf("column %q not part of %v", name, header)
		}
		lines = lines[1:]
		origLines := lines

		tryUntil := time.Now().Add(*wait)
		for {
			lines = origLines

			equal := true
			var diff bytes.Buffer
			w := tabwriter.NewWriter(&diff, 5, 4, 3, ' ', 0)

			fmt.Fprintf(w, "  %s\n", strings.Join(zero.TableHeader(), "\t"))

			for obj := range tbl.All(db.ReadTxn()) {
				rowRaw := takeColumns(obj.TableRow(), columnIndexes)
				row := strings.Join(rowRaw, "\t")
				if len(lines) == 0 {
					equal = false
					fmt.Fprintf(w, "- %s\n", row)
					continue
				}
				line := lines[0]
				splitLine := splitByPositions(ts, line, columnPositions)

				if !slices.Equal(rowRaw, splitLine) {
					fmt.Fprintf(w, "  %s\n", row)
				} else {
					fmt.Fprintf(w, "- %s\n", row)
					fmt.Fprintf(w, "+ %s\n", line)
					equal = false
				}
				lines = lines[1:]
			}
			for _, line := range lines {
				fmt.Fprintf(w, "+ %s\n", line)
				equal = false
			}

			if equal {
				break
			}
			w.Flush()
			if time.Now().After(tryUntil) {
				ts.Fatalf("table mismatch (check formatting!):\n%s", diff.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func takeColumns[T any](xs []T, idxs []int) []T {
	// Assuming idxs is sorted.
	for i, idx := range idxs {
		xs[i] = xs[idx]
	}
	return xs[:len(idxs)]
}

func InsertCmd[Obj any](db *statedb.DB, tbl statedb.RWTable[Obj]) Cmd {
	return func(ts *testscript.TestScript, neg bool, args []string) {
		if len(args) == 0 {
			ts.Fatalf("usage: insert path...")
		}
		wtxn := db.WriteTxn(tbl)
		defer wtxn.Commit()
		for _, arg := range args {
			data := ts.ReadFile(arg)
			var obj Obj
			if err := yaml.Unmarshal([]byte(data), &obj); err != nil {
				ts.Fatalf("Unmarshal(%s): %s", arg, err)
			}
			_, _, err := tbl.Insert(wtxn, obj)
			if err != nil {
				ts.Fatalf("Insert(%s): %s", arg, err)
			}
		}
	}
}

func DeleteCmd[Obj any](db *statedb.DB, tbl statedb.RWTable[Obj]) Cmd {
	return func(ts *testscript.TestScript, neg bool, args []string) {
		if len(args) == 0 {
			ts.Fatalf("usage: delete path...")
		}
		wtxn := db.WriteTxn(tbl)
		defer wtxn.Commit()
		for _, arg := range args {
			data := ts.ReadFile(arg)
			var obj Obj
			if err := yaml.Unmarshal([]byte(data), &obj); err != nil {
				ts.Fatalf("Unmarshal(%s): %s", arg, err)
			}
			_, _, err := tbl.Delete(wtxn, obj)
			if err != nil {
				ts.Fatalf("Delete(%s): %s", arg, err)
			}
		}
	}
}

func splitHeaderLine(line string) (names []string, pos []int) {
	start := 0
	skip := true
	for i, r := range line {
		switch r {
		case ' ', '\t':
			if !skip {
				names = append(names, line[start:i])
				pos = append(pos, start)
				start = -1
			}
			skip = true
		default:
			skip = false
			if start == -1 {
				start = i
			}
		}
	}
	if start >= 0 && start < len(line) {
		names = append(names, line[start:])
		pos = append(pos, start)
	}
	return
}

func splitByPositions(ts *testscript.TestScript, line string, positions []int) []string {
	out := make([]string, 0, len(positions))
	start := 0
	for _, pos := range positions {
		if pos > len(line) {
			ts.Fatalf("position %d exceeds line %q, check your table formatting", pos, line)
		}
		out = append(out, line[start:pos])
		start = pos
	}
	return out
}
