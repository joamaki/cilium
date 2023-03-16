package tables

import (
	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/memdb/tables/services"
	"github.com/cilium/cilium/pkg/hive/cell"
)

// Type aliases for all tables
type (
	ServiceTable = state.Table[*services.Service]
)

var Cell = cell.Module(
	"tables",
	"Table definitions for in-memory state",

	services.Cell,
)
