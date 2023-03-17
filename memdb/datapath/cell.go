package datapath

import (
	"github.com/cilium/cilium/memdb/datapath/controllers"
	"github.com/cilium/cilium/memdb/datapath/maps"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var Cell = cell.Module(
	"datapath",
	"Datapath",

	cell.ProvidePrivate(func() *maps.ServiceMap { return &maps.ServiceMap{} }),
	controllers.Cell,
)
