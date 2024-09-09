// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package testutils

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/cilium/statedb"
)

// DumpTable writes out a StateDB table to the given writer.
// The StateDB table must implement [TableWritable], e.g. have methods
// TableRow and TableHeader.
func DumpTable[Obj statedb.TableWritable](txn statedb.ReadTxn, tbl statedb.Table[Obj], w io.Writer) {
	var zero Obj
	tw := tabwriter.NewWriter(w, 5, 0, 3, ' ', 0)
	fmt.Fprintf(tw, "--- %s ---\n", tbl.Name())
	fmt.Fprintln(tw, strings.Join(zero.TableHeader(), "\t"))
	for obj := range tbl.All(txn) {
		fmt.Fprintln(tw, strings.Join(obj.TableRow(), "\t"))
	}
	tw.Flush()
}
