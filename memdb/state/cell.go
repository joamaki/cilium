package state

import "github.com/cilium/cilium/pkg/hive/cell"

var Cell = cell.Module(
	"state",
	"Das State",

	cell.Provide(New),
)
