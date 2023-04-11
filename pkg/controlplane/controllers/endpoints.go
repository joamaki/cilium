package controllers

import (
	"context"
	"fmt"
	"net"

	"github.com/cilium/cilium/pkg/controlplane/tables"
	"github.com/cilium/cilium/pkg/endpoint"
	"github.com/cilium/cilium/pkg/endpointmanager"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/identity"
	cilium_api_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/resource"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/statedb"
)

var endpointsControllerCell = cell.Group(
	cell.Invoke(registerEndpointsController),
	cell.Provide(ciliumEndpointsResource), // FIXME dup pkg/k8s/watchers/cilium_endpoint.go.
)

type endpointsSubscriber struct {
	db    statedb.DB
	table statedb.Table[*tables.Endpoint]
}

// EndpointCreated implements endpointmanager.Subscriber
func (epsub *endpointsSubscriber) EndpointCreated(localEP *endpoint.Endpoint) {
	txn := epsub.db.WriteTxn()
	w := epsub.table.Writer(txn)

	ep, err := w.First(tables.EndpointByName(localEP.K8sNamespace, localEP.K8sPodName))
	if err != nil {
		panic(err)
	}
	if ep == nil {
		ep = &tables.Endpoint{
			Namespace: localEP.K8sNamespace,
			Name:      localEP.K8sPodName,
			NodeIP:    net.ParseIP(node.GetCiliumEndpointNodeIP()),
			Identity:  0, // TODO not allocated yet.
		}
	}
	ep.Local = localEP

	fmt.Printf(">>> Upserted local endpoint %s/%s\n", ep.Namespace, ep.Name)
	w.Insert(ep)
	txn.Commit()
}

// EndpointDeleted implements endpointmanager.Subscriber
func (epsub *endpointsSubscriber) EndpointDeleted(ep *endpoint.Endpoint, conf endpoint.DeleteConfig) {
	txn := epsub.db.WriteTxn()
	epsub.table.Writer(txn).Delete(&tables.Endpoint{Namespace: ep.K8sNamespace, Name: ep.K8sPodName})
	txn.Commit()
}

func (epsub *endpointsSubscriber) loop(eps resource.Resource[*cilium_api_v2.CiliumEndpoint]) {
	// TODO batching
	for ev := range eps.Events(context.TODO()) {
		if ev.Object != nil {
			if ev.Object.Status.Networking.NodeIP == node.GetCiliumEndpointNodeIP() {
				// Ignore local endpoints.
				continue
			}
		}

		txn := epsub.db.WriteTxn()
		w := epsub.table.Writer(txn)
		ep, err := w.First(tables.EndpointByName(ev.Key.Namespace, ev.Key.Name))
		if err != nil {
			panic(err)
		}
		if ep == nil {
			ep = &tables.Endpoint{
				Namespace: ev.Key.Namespace,
				Name:      ev.Key.Name,
			}
		}

		switch ev.Kind {
		case resource.Upsert:
			obj := ev.Object
			ep.Identity = identity.NumericIdentity(obj.Status.Identity.ID)
			ep.Labels = labels.Map2Labels(obj.Labels, "k8s")
			n := obj.Status.Networking
			ep.NodeIP = net.ParseIP(n.NodeIP)
			ep.Addresses = make([]net.IP, 0, len(n.Addressing))
			for _, addr := range n.Addressing {
				if addr.IPV4 != "" {
					ep.Addresses = append(ep.Addresses, net.ParseIP(addr.IPV4))
				}
				if addr.IPV6 != "" {
					ep.Addresses = append(ep.Addresses, net.ParseIP(addr.IPV6))
				}
			}
			fmt.Printf(">>> Upserted remote endpoint %s/%s\n", ep.Namespace, ep.Name)
			if err := w.Insert(ep); err != nil {
				panic(err)
			}
		case resource.Delete:
			if err := w.Delete(ep); err != nil {
				panic(err)
			}
		}
		txn.Commit()
		ev.Done(nil)
	}
}

var _ endpointmanager.Subscriber = &endpointsSubscriber{}

func registerEndpointsController(
	em endpointmanager.EndpointManager, eps resource.Resource[*cilium_api_v2.CiliumEndpoint],
	db statedb.DB, epTable statedb.Table[*tables.Endpoint]) {

	epsub := &endpointsSubscriber{db, epTable}
	// Subscribe to local endpoints
	em.Subscribe(epsub)

	// And remote endpoints
	go epsub.loop(eps)
}

func ciliumEndpointsResource(lc hive.Lifecycle, cs client.Clientset) (resource.Resource[*cilium_api_v2.CiliumEndpoint], error) {
	if !cs.IsEnabled() {
		return nil, nil
	}
	lw := utils.ListerWatcherFromTyped[*cilium_api_v2.CiliumEndpointList](cs.CiliumV2().CiliumEndpoints(""))
	return resource.New[*cilium_api_v2.CiliumEndpoint](lc, lw), nil
}
