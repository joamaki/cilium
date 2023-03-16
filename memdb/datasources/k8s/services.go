package k8s

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/memdb/state/structs"
	"github.com/cilium/cilium/memdb/tables"
	svcs "github.com/cilium/cilium/memdb/tables/services"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s/client"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/stream"
	"github.com/sirupsen/logrus"
)

var Cell = cell.Invoke(servicesDataSource)

func servicesDataSource(lc hive.Lifecycle, log logrus.FieldLogger, client client.Clientset, st *state.State, services tables.ServiceTable) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	lc.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			syncServices(ctx, &wg, log, client, st, services)
			return nil
		},
		OnStop: func(hive.HookContext) error {
			cancel()
			wg.Wait()
			return nil
		},
	})
}

func syncServices(ctx context.Context, wg *sync.WaitGroup, log logrus.FieldLogger, client client.Clientset, st *state.State, serviceTable tables.ServiceTable) {
	type EventBuffer map[string]*slim_corev1.Service
	const bufferSize = 64
	src := stream.BufferBy(
		K8sEvents(utils.ListerWatcherFromTyped[*slim_corev1.ServiceList](client.Slim().CoreV1().Services(""))),
		// Buffer into a bucket of 64 items. Wait at most 100ms to fill a bucket.
		bufferSize, 100*time.Millisecond,

		// Buffer the events into a map, coalescing them by key.
		func(buf EventBuffer, ev Event) EventBuffer {
			if buf == nil {
				buf = make(map[string]*slim_corev1.Service, bufferSize)
			}
			var svc *slim_corev1.Service
			if ev.Type != TypeDelete {
				svc = ev.Obj.(*slim_corev1.Service)
			}
			buf[svc.Namespace+"/"+svc.Name] = svc
			return buf
		},

		func(buf EventBuffer) EventBuffer {
			return make(map[string]*slim_corev1.Service, bufferSize)
		},
	)

	onEvents := func(events EventBuffer) {
		tx := st.Write()
		for _, k8sSvc := range events {
			if k8sSvc != nil {
				services := serviceTable.Modify(tx)
				svc, _ := services.First(state.ByName(k8sSvc.Namespace, k8sSvc.Name))
				if svc != nil {
					svc = svc.DeepCopy()
				} else {
					svc = &svcs.Service{
						ExtMeta: structs.ExtMetaFromK8s(k8sSvc),
						Type:    k8sSvc.Spec.Type,
					}
					svc.ExtMeta.ID = structs.NewUUID() // XXX
				}
				svc.Ports = servicePorts(k8sSvc)
				svc.IPs = serviceIPs(k8sSvc)
				if err := services.Insert(svc); err != nil {
					// NOTE: Only fails if schema is bad.
					log.WithError(err).Error("services.Insert")
				} else {
					log.Infof("Imported service %s/%s", svc.Namespace, svc.Name)
				}
			} else {
				services := serviceTable.Modify(tx)
				svc, _ := services.First(state.ByName(k8sSvc.Namespace, k8sSvc.Name))
				if svc != nil {
					if err := services.Delete(svc); err != nil {
						// NOTE: Only fails if schema is bad.
						log.WithError(err).Error("services.Delete")
					} else {
						log.Infof("Deleted service %s/%s", svc.Namespace, svc.Name)
					}
				}
			}
		}
		if err := tx.Commit(); err != nil {
			// TODO
			panic(err)
		}
	}

	wg.Add(1)

	src.Observe(
		ctx,
		onEvents,
		func(err error) { wg.Done() },
	)
}

func servicePorts(svc *slim_corev1.Service) (ports []uint16) {
	// FIXME protocol
	for _, port := range svc.Spec.Ports {
		ports = append(ports, uint16(port.Port))
	}
	return
}

func serviceIPs(svc *slim_corev1.Service) (ips []structs.IPAddr) {
	if svc.Spec.ClusterIP != "" {
		ips = append(ips, structs.IPAddr{Addr: netip.MustParseAddr(svc.Spec.ClusterIP)})
	}
	for _, ip := range svc.Spec.ClusterIPs {
		ips = append(ips, structs.IPAddr{Addr: netip.MustParseAddr(ip)})
	}
	return
}
