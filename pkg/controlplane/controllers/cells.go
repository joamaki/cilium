package controllers

import (
	"github.com/cilium/cilium/pkg/hive/cell"
)

var Cell = cell.Module(
	"controlplane-controllers",
	"Control-plane state controllers",

	identitiesControllerCell,
	endpointsControllerCell,
)
