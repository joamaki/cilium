// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"github.com/cilium/hive/cell"
	"github.com/cilium/statedb"
)

// Cell provides the [Services] API for configuring load-balancing and the
// Table[Frontend] and Table[Backend] for read-only access to service frontends
// and backends.
//
// This is split into two cells for testing purposes.
var Cell = cell.Group(
	ServicesCell,
	ReconcilerCell,
)

var ServicesCell = cell.Module(
	"services",
	"Service load-balancing structures and tables",

	cell.Config(DefaultConfig),

	// Provide the RWTable[Frontend] and RWTable[Backend] privately to this
	// module so that the tables are only modified via the Services API.
	cell.ProvidePrivate(
		NewFrontendsTable,
		NewBackendsTable,
	),

	cell.Provide(
		NewServices,

		// Provide Table[Frontend] and Table[Backend] to the outside for
		// read access.
		statedb.RWTable[*Frontend].ToTable,
		statedb.RWTable[*Backend].ToTable,
	),
)
