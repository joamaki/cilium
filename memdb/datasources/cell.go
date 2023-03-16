package datasources

import (
	"github.com/cilium/cilium/memdb/datasources/etcd"
	"github.com/cilium/cilium/memdb/datasources/k8s"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var Cell = cell.Module(
	"datasources",
	"State data sources",

	//k8sCell,
	//dummyCell,

	k8s.Cell,
	etcd.Cell,
)
