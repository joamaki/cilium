package cache

import (
	"context"
	"net"

	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/lock"
)

func (sc *serviceCache) Observe(ctx context.Context, next func(*ServiceEvent), complete func(error)) {
	sc.src.Observe(ctx, next, complete)
}

func (sc *serviceCache) GetEndpointsOfService(svcID k8s.ServiceID) *k8s.Endpoints {
	sc.synchronized.Wait()
	panic("unimplemented")
}

func (sc *serviceCache) GetServiceFrontendIP(svcID k8s.ServiceID, svcType loadbalancer.SVCType) net.IP {
	sc.synchronized.Wait()
	panic("unimplemented")
}

func (sc *serviceCache) GetServiceIP(svcID k8s.ServiceID) *loadbalancer.L3n4Addr {
	sc.synchronized.Wait()
	panic("unimplemented")
}

func (sc *serviceCache) EnsureService(svcID k8s.ServiceID, swg *lock.StoppableWaitGroup) bool {
	sc.synchronized.Wait()
	panic("unimplemented")
}

func (sc *serviceCache) GetServiceAddrsWithType(svcID k8s.ServiceID, svcType loadbalancer.SVCType) (map[loadbalancer.FEPortName][]*loadbalancer.L3n4Addr, int) {
	sc.synchronized.Wait()
	panic("unimplemented")
}
