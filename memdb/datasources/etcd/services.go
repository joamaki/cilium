package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/memdb/tables"
	"github.com/cilium/cilium/memdb/tables/services"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/rate"
	"github.com/cilium/cilium/pkg/stream"
	"github.com/sirupsen/logrus"
)

var Cell = cell.Invoke(servicesDataSource)

func servicesDataSource(lc hive.Lifecycle, log logrus.FieldLogger, st *state.State, services tables.ServiceTable) {
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			go syncServicesFrom(ctx, &wg, log, st, services)
			go syncServicesTo(ctx, &wg, log, st, services)
			return nil
		},
		OnStop: func(hive.HookContext) error {
			cancel()
			wg.Wait()
			return nil
		},
	})
}

func serviceKey(svc *services.Service) []byte {
	return []byte("services/" + svc.Namespace + "/" + svc.Name)
}

func parseServiceKey(bs []byte) (namespace, name string) {
	fmt.Sscanf(string(bs), "services/%s/%s", &namespace, &name)
	return
}

func syncServicesFrom(ctx context.Context, wg *sync.WaitGroup, log logrus.FieldLogger, st *state.State, serviceTable tables.ServiceTable) {
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
			WatchObservable(client, "services"),
			// Always retry, with exponential backoff, up to 1 minute.
			stream.BackoffRetry(retry, 100*time.Millisecond, time.Minute))

	onEvents := func(events []Event) {
		tx := st.Write()
		svcs := serviceTable.Modify(tx)

		for _, ev := range events {
			if !ev.IsDelete {
				kv := ev.Kv
				var svc services.Service
				if err := json.Unmarshal(kv.Value, &svc); err != nil {
					log.WithError(err).Error("Unmarshaling failed")
					continue
				}
				if svc.Source != services.ServiceSourceEtcd {
					continue
				}
				if current, _ := svcs.First(state.ByName(svc.Namespace, svc.Name)); current != nil {
					if svc.Revision <= current.Revision {
						continue
					}
				}
				if err := svcs.Insert(&svc); err != nil {
					// NOTE: Only fails if schema is bad.
					log.WithError(err).Error("services.Insert")
				} else {
					log.Infof("Imported 'etcd' service: %s/%s", svc.Namespace, svc.Name)
				}
			} else {
				namespace, name := parseServiceKey(ev.Kv.Key)
				svc, _ := svcs.First(state.ByName(namespace, name))
				if svc != nil {
					if err := svcs.Delete(svc); err != nil {
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

func revLess(a, b string) bool {
	aNum, _ := strconv.ParseUint(a, 10, 64)
	bNum, _ := strconv.ParseUint(b, 10, 64)
	return aNum < bNum
}

func syncServicesTo(ctx context.Context, wg *sync.WaitGroup, log logrus.FieldLogger, st *state.State, serviceTable tables.ServiceTable) {
	wg.Add(1)
	defer wg.Done()

	minBackoff := time.Second
	maxBackoff := 10 * time.Second
	backoff := minBackoff

	client := etcdClient("127.0.0.1:4002")
	if client == nil {
		return
	}

	lim := rate.NewLimiter(time.Second, 20)
	defer lim.Stop()

	nextRevision := "0"
	servicesChanged :=
		stream.Trigger(
			ctx,
			stream.Filter(
				st.Observable,
				func(e state.Event) bool {
					return e.ForTable(serviceTable.Name())
				}))

	for {
		tx := st.Read()
		svcs := serviceTable.Read(tx)

		// Get all K8s services newer than last successfully processed revision.
		iter, err := svcs.LowerBound(services.BySourceAndRevision(services.ServiceSourceK8s, nextRevision))
		if err != nil {
			log.Errorf("Services.LowerBound failed: %w", err)
			continue
		}

		newRevision := nextRevision

		err = state.ProcessEach(
			iter,
			func(svc *services.Service) error {
				if err := lim.Wait(ctx); err != nil {
					return err
				}

				log.Infof("Syncing service %s/%s from %s", svc.Namespace, svc.Name, svc.Source)

				// Remember the highest seen revision for the next round.
				if revLess(newRevision, svc.Revision) {
					newRevision = svc.Revision
				}
				bs, err := json.Marshal(svc)
				if err != nil {
					return err
				}
				_, err = client.Put(ctx, string(serviceKey(svc)), string(bs))
				return err
			},
		)
		if err != nil {
			log.WithError(err).Errorf("Exporting services failed, retrying in %.2fs", float64(backoff)/float64(time.Second))
			time.Sleep(backoff)
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		} else {
			// Naive error handling here, we might export objects that were
			// already exported.
			nextRevision = newRevision
			backoff = minBackoff
		}

		select {
		case <-ctx.Done():
			return
		case <-servicesChanged:
		}
	}
}
