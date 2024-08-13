package experimental

import (
	"context"
	"log/slog"

	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/kvstore"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/service/store"
	"github.com/cilium/cilium/pkg/source"
	"github.com/cilium/cilium/pkg/time"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/stream"
)

var kvstoreCell = cell.Module(
	"kvstore",
	"Reflects load-balancing state from KVStore",

	cell.ProvidePrivate(clusterServiceEvents),
	cell.Invoke(registerKVStoreReflector),
)

type clusterServiceEvent = kvstore.TypedKeyValueEvent[*store.ClusterService]

func clusterServiceEvents() stream.Observable[clusterServiceEvent] {
	decode := func(value []byte) (*store.ClusterService, bool) {
		s := &store.ClusterService{}
		err := s.Unmarshal("", value)
		return s, err == nil
	}
	return stream.FuncObservable[clusterServiceEvent](
		func(ctx context.Context, next func(kvstore.TypedKeyValueEvent[*store.ClusterService]), complete func(error)) {
			if !option.Config.JoinCluster {
				complete(nil)
				return
			}
			client := kvstore.Client()
			kvstore.EventStream(client, store.ServiceStorePrefix, decode).Observe(ctx, next, complete)
		})
}

type kvstoreReflectorParams struct {
	cell.In

	Log       *slog.Logger
	Lifecycle cell.Lifecycle
	JobGroup  job.Group
	Events    stream.Observable[clusterServiceEvent]
	Writer    *Writer
}

func registerKVStoreReflector(p kvstoreReflectorParams) {
	if !p.Writer.IsEnabled() {
		return
	}
	initComplete := p.Writer.RegisterInitializer("kvstore")
	p.JobGroup.Add(job.OneShot("reflector", func(ctx context.Context, health cell.Health) error {
		runKVStoreReflector(ctx, p, initComplete)
		return nil
	}))
}

func runKVStoreReflector(ctx context.Context, p kvstoreReflectorParams, initComplete func(WriteTxn)) {
	const (
		bufferSize = 300
		waitTime   = 10 * time.Millisecond
	)
	events := stream.ToChannel(
		ctx,
		stream.Buffer(
			p.Events,
			bufferSize, waitTime,
			bufferKVStoreEvent,
		),
	)

	for {
		select {
		case <-ctx.Done():
			// Drain & stop.
			for range events {
			}
			return
		case buf, ok := <-events:
			if !ok {
				return
			}
			txn := p.Writer.WriteTxn()
			for _, ev := range buf {
				cs := ev.Value
				var name loadbalancer.ServiceName
				if cs != nil {
					name.Cluster = cs.Cluster
					name.Namespace = cs.Namespace
					name.Name = cs.Name
				}
				switch ev.Typ {
				case kvstore.EventTypeListDone:
					initComplete(txn)
				case kvstore.EventTypeDelete:
					p.Writer.DeleteServiceAndFrontends(txn, name, source.KVStore)
					p.Writer.ReleaseBackendsFromSource(txn, name, source.KVStore)
				case kvstore.EventTypeCreate, kvstore.EventTypeModify:
					svc, fes, bes := parseClusterService(name, cs)
					p.Writer.UpsertServiceAndFrontends(txn, svc, fes...)
					p.Writer.UpsertBackends(txn, name, source.KVStore, bes...)
				}
				txn.Commit()
			}
		}
	}
}

func bufferKVStoreEvent(buf map[string]clusterServiceEvent, ev clusterServiceEvent) map[string]clusterServiceEvent {
	if buf == nil {
		buf = map[string]clusterServiceEvent{}
	}
	buf[ev.Key] = ev
	return buf
}

func parseClusterService(name loadbalancer.ServiceName, cs *store.ClusterService) (svc *Service, fes []FrontendParams, bes []BackendParams) {
	svc = &Service{
		Name:             name,
		Source:           source.KVStore,
		Labels:           labels.Map2Labels(cs.Labels, string(source.KVStore)),
		NatPolicy:        loadbalancer.SVCNatPolicyNone,
		ExtTrafficPolicy: loadbalancer.SVCTrafficPolicyCluster,
		IntTrafficPolicy: loadbalancer.SVCTrafficPolicyCluster,
	}
	for ip, ports := range cs.Frontends {
		addrCluster := cmtypes.MustParseAddrCluster(ip)
		for portName, port := range ports {
			l4 := *loadbalancer.NewL4Addr(loadbalancer.L4Type(port.Protocol), uint16(port.Port))
			fe := FrontendParams{
				Address:     loadbalancer.L3n4Addr{AddrCluster: addrCluster, L4Addr: l4},
				Type:        loadbalancer.SVCTypeClusterIP,
				ServiceName: name,
				PortName:    loadbalancer.FEPortName(portName),
			}
			fes = append(fes, fe)
		}
	}
	for ip, ports := range cs.Backends {
		nodeName := cs.Hostnames[ip]
		addrCluster := cmtypes.MustParseAddrCluster(ip)
		for portName, port := range ports {
			l4 := *loadbalancer.NewL4Addr(loadbalancer.L4Type(port.Protocol), uint16(port.Port))
			be := BackendParams{
				L3n4Addr: loadbalancer.L3n4Addr{AddrCluster: addrCluster, L4Addr: l4},
				PortName: portName,
				NodeName: nodeName,
				State:    loadbalancer.BackendStateActive,
			}
			bes = append(bes, be)
		}
	}
	return
}
