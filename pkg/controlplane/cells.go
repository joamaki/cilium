package controlplane

import (
	"github.com/cilium/cilium/pkg/controlplane/controllers"
	"github.com/cilium/cilium/pkg/controlplane/tables"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var Cell = cell.Module(
	"controlplane",
	"Cilium agent control-plane",

	controllers.Cell,
	tables.Cell,
)
