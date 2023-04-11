package tables

import (
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/statedb"
)

var Cell = cell.Module(
	"controlplane-tables",
	"State tables for control-plane",

	statedb.NewTableCell[*Identity](identityTableSchema),
	statedb.NewTableCell[*Endpoint](endpointTableSchema),
)
