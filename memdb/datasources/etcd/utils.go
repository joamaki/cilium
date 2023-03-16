package etcd

import (
	"context"
	"fmt"
	"time"

	"github.com/cilium/cilium/pkg/stream"
	mvccpb "go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/mirror"
)

type Event struct {
	IsDelete bool
	Kv       *mvccpb.KeyValue
}

func WatchObservable(c *clientv3.Client, prefix string) stream.Observable[[]Event] {
	s := mirror.NewSyncer(c, prefix, 0)
	return stream.FuncObservable[[]Event](
		func(ctx context.Context, next func([]Event), complete func(err error)) {
			go func() {
				getResps, errs := s.SyncBase(ctx)
			syncLoop:
				for {
					select {
					case resp, ok := <-getResps:
						if !ok {
							break syncLoop

						}
						events := make([]Event, len(resp.Kvs))
						for i, kv := range resp.Kvs {
							events[i].Kv = kv
						}
						next(events)
					case err := <-errs:
						complete(err)
						return
					}
				}

				var resp clientv3.WatchResponse
				for resp = range s.SyncUpdates(ctx) {
					if resp.Canceled {
						break
					}
					events := make([]Event, len(resp.Events))
					for i, ev := range resp.Events {
						events[i].IsDelete = !(ev.IsCreate() || ev.IsModify())
						events[i].Kv = ev.Kv
					}
					next(events)
				}
				complete(resp.Err())
			}()
		})
}

func etcdClient(addr string) *clientv3.Client {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{addr},
		DialTimeout: 1 * time.Second,
	})
	if err != nil {
		fmt.Printf("etcdClient: New failed: %s\n", err)
		return nil
	}
	/*
		_, err = cli.Put(context.TODO(), "foo", "bar")
		if err != nil {
			fmt.Printf("etcdClient: Put failed %s\n", err)
			return nil
		}*/
	//defer cli.Close()
	return cli
}
