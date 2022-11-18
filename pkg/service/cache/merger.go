package cache

import (
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/service/store"
)

func (sc *serviceCache) MergeExternalServiceUpdate(service *store.ClusterService, swg *lock.StoppableWaitGroup) {
	panic("TBD")
}

func (sc *serviceCache) MergeExternalServiceDelete(service *store.ClusterService, swg *lock.StoppableWaitGroup) {
	panic("TBD")
}

func (sc *serviceCache) MergeClusterServiceUpdate(service *store.ClusterService, swg *lock.StoppableWaitGroup) {
	panic("TBD")
}

func (sc *serviceCache) MergeClusterServiceDelete(service *store.ClusterService, swg *lock.StoppableWaitGroup) {
	panic("TBD")
}
