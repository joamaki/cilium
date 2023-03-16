package etcd

import (
	"context"
	"sync"
	"time"

	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/memdb/state/structs"
	"github.com/cilium/cilium/memdb/tables"
	svcs "github.com/cilium/cilium/memdb/tables/services"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/stream"
	"github.com/sirupsen/logrus"
)

var Cell = cell.Invoke(servicesDataSource)

func servicesDataSource(lc hive.Lifecycle, log logrus.FieldLogger, st *state.State, services tables.ServiceTable) {
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			syncServices(ctx, &wg, log, st, services)
			return nil
		},
		OnStop: func(hive.HookContext) error {
			cancel()
			wg.Wait()
			return nil
		},
	})
}

func syncServices(ctx context.Context, wg *sync.WaitGroup, log logrus.FieldLogger, st *state.State, serviceTable tables.ServiceTable) {
	client := etcdClient("127.0.0.1:4002")
	if client == nil {
		return
	}

	retry := func(err error) bool {
		log.WithError(err).Error("Watcher failed, restarting")
		return true
	}

	src :=
		stream.Retry(
			WatchObservable(client, ""),
			// Always retry, with exponential backoff, up to 1 minute.
			stream.BackoffRetry(retry, 100*time.Millisecond, time.Minute))

	onEvents := func(events []Event) {
		tx := st.Write()
		services := serviceTable.Modify(tx)

		for _, ev := range events {
			if !ev.IsDelete {
				kv := ev.Kv
				svc, _ := services.First(state.ByName("etcd", string(kv.Key)))
				if svc != nil {
					svc = svc.DeepCopy()
				} else {
					svc = &svcs.Service{}
					svc.ExtMeta.Namespace = "etcd"
					svc.ExtMeta.Name = string(kv.Key)
					svc.ExtMeta.ID = structs.NewUUID() // XXX
				}
				if err := services.Insert(svc); err != nil {
					// NOTE: Only fails if schema is bad.
					log.WithError(err).Error("services.Insert")
				} else {
					log.Infof("Imported 'etcd' service: %s/%s", svc.Namespace, svc.Name)
				}
			} else {
				svc, _ := services.First(state.ByName("etcd", string(ev.Kv.Key)))
				if svc != nil {
					if err := services.Delete(svc); err != nil {
						// NOTE: Only fails if schema is bad.
						log.WithError(err).Error("services.Delete")
					} else {
						log.Infof("Deleted 'etcd' service %s/%s", svc.Namespace, svc.Name)
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
