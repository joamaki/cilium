package controlplane

import (
	"github.com/cilium/cilium/lbtest/controlplane/services"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var Cell = cell.Module(
	"lbtest-controlplane",
	"Control-plane for lbtest",

	services.K8sHandlerCell,
	services.ServiceManagerCell,

	//redirectpolicies.Cell,
)
