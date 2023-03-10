package datasources

import "github.com/cilium/cilium/pkg/hive/cell"

var Cell = cell.Module(
	"datasources",
	"State data sources",

	k8sCell,
)
