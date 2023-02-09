package api

import (
	"context"
	"net"

	lb "github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/stream"
)

type Datapath interface {
	LoadBalancer
	Devices
}

type Device struct {
	Name string
	IPs  []net.IP
}

type Devices interface {
	stream.Observable[[]Device]
	Devices(ctx context.Context) <-chan []Device
}

type LoadBalancer interface {
	Upsert(svc *lb.SVC)
	Delete(addr lb.L3n4Addr)
	GarbageCollect()
}
