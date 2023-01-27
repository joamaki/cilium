package main

import (
	"github.com/cilium/cilium/controlplane/servicemanager"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var ControlPlane = cell.Module(
	"lbtest-controlplane",
	"Control-plane for lbtest",

	apiserverCell,

	// Service load-balancing
	servicemanager.K8sHandlerCell,
	servicemanager.Cell,
	servicemanager.APIHandlersCell,
)
