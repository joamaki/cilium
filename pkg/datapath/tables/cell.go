package tables

import (
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/statedb"
)

var Cell = cell.Module(
	"datapath-tables",
	"Datapath state tables",

	statedb.NewTableCell[*Device](deviceTableSchema),
)
