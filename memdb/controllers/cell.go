package controllers

import (
	"github.com/cilium/cilium/memdb/controllers/identity"
	"github.com/cilium/cilium/memdb/controllers/policy"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var Cell = cell.Module(
	"controllers",
	"Controllers watch and update the state",

	policy.Cell,
	identity.Cell,
)
