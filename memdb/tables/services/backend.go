package services

import "github.com/cilium/cilium/memdb/state/structs"

type Backend struct {
	Backend structs.IPAddr
	Port    uint16
}
