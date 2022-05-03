package service

import (
	"context"
	"net"
	"sync"

	"github.com/cilium/cilium/pkg/eventsource"
	"github.com/cilium/cilium/pkg/k8s"
	"go.uber.org/fx"
	v1 "k8s.io/api/core/v1"
)

var ServiceReactorModule = fx.Module(
	"service-reactor",
	fx.Provide(newServiceReactor),
)

type ServiceReactor struct {
	servicesResource k8s.K8sResource[*v1.Service]
	addrsSource eventsource.EventSource[NodeAddresses]

	stopChan chan struct{}
	wg sync.WaitGroup
}

func newServiceReactor(lc fx.Lifecycle, servicesResource k8s.K8sResource[*v1.Service]) *ServiceReactor {
	sr := &ServiceReactor{
		servicesResource: servicesResource,
		stopChan: make(chan struct{}),
	}
	lc.Append(fx.Hook{OnStart: sr.onStart, OnStop: sr.onStop})
	return sr
}

func (sr *ServiceReactor) onStart(context.Context) error {
	sr.wg.Add(1)
	go sr.reactor()
	return nil
}

func (sr *ServiceReactor) onStop(context.Context) error {
	close(sr.stopChan)
	sr.wg.Wait()
	return nil
}

func (sr *ServiceReactor) reactor() {

	serviceChangesChan, unsubServices := sr.servicesResource.ChangedKeys().SubscribeChan("service-reactor", 16)
	defer unsubServices()
 
	var addrs NodeAddresses
	newAddrsChan, unsubAddrs := sr.addrsSource.SubscribeChan("service-reactor", 16)
	defer unsubAddrs()

	for {
		select {
		case key := <-serviceChangesChan:
			svc, err := sr.servicesResource.Get(key)
			if err == nil {
				sr.handleChangedService(svc, &addrs)
			} else {
				// handle error
			}

		case newAddrs := <-newAddrsChan:
			addrs = newAddrs
			sr.updateServices(&addrs)
			

		case <-sr.stopChan:
			return
		}
	}
}

func (sr *ServiceReactor) handleChangedService(svc *v1.Service, addrs *NodeAddresses) {
	// do something
}

func (sr *ServiceReactor) updateServices(addrs *NodeAddresses) {
	// go and update services after the node addresses had changed.
}


// devices module would provide these:
type NodeAddresses struct {
	IPv4 []net.IP
	IPv6 []net.IP
}
