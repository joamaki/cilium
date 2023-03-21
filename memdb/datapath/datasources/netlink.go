package datasources

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	dptables "github.com/cilium/cilium/memdb/datapath/tables"
	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/stream"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// Netlink datasource populates the routes and devices tables and
// reconciles the custom-routes table.

var Cell = cell.Module(
	"datapath-datasources-netlink",
	"Netlink datasource synchronizes routes and devices",

	cell.Invoke(
		netlinkDataSource,
	),
)

func netlinkDataSource(lc hive.Lifecycle, log logrus.FieldLogger,
	st *state.State,
	routes state.Table[*dptables.Route], customRoutes state.Table[*dptables.CustomRoute],
	devices state.Table[*dptables.Device]) {

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			if err := syncRoutes(ctx, &wg, log, st, routes); err != nil {
				log.WithError(err).Error("syncRoutes")
				return err
			}
			if err := syncCustomRoutes(ctx, &wg, log, st, routes, customRoutes); err != nil {
				log.WithError(err).Error("syncCustomRoutes")
				return err
			}
			if err := syncDevices(ctx, &wg, log, st, devices); err != nil {
				log.WithError(err).Error("syncDevices")

			}
			return nil
		},
		OnStop: func(hive.HookContext) error {
			cancel()
			wg.Wait()
			return nil
		},
	})
}

func parseRoute(revision uint64, r netlink.Route) *dptables.Route {
	return &dptables.Route{
		Revision: revision,

		Prefix:   r.Dst,
		Nexthop:  r.Gw,
		Local:    r.Src,
		IfIndex:  r.LinkIndex,
		MTU:      r.MTU,
		Priority: r.Priority,
		Proto:    int(r.Protocol),
		Scope:    r.Scope,
		Table:    r.Table,
		Type:     r.Type,
	}

}

func syncRoutes(ctx context.Context, wg *sync.WaitGroup, log logrus.FieldLogger, st *state.State, routes state.Table[*dptables.Route]) error {
	revision := uint64(0)
	routeChan := make(chan netlink.RouteUpdate)
	err := netlink.RouteSubscribe(routeChan, ctx.Done())
	if err != nil {
		return fmt.Errorf("RouteSubscribe: %w", err)
	}

	{
		initialRoutes, err := netlink.RouteList(nil, netlink.FAMILY_ALL)
		if err != nil {
			return fmt.Errorf("RouteList: %w", err)
		}
		tx := st.Write()
		routesTx := routes.Modify(tx)
		for _, r := range initialRoutes {
			if err := routesTx.Insert(parseRoute(revision, r)); err != nil {
				log.WithError(err).Error("Inserting route failed")
			}
		}
		log.Infof("Inserted %d routes", len(initialRoutes))
		tx.Commit()
		revision++
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for update := range routeChan {
			// TODO: batching/delaying.
			tx := st.Write()
			routesTx := routes.Modify(tx)
			if update.Type == unix.RTM_DELROUTE {
				routesTx.Delete(parseRoute(0, update.Route))
			} else {
				routesTx.Insert(parseRoute(revision, update.Route))
			}
			tx.Commit()
			revision++
		}
	}()
	return nil
}

func syncCustomRoutes(ctx context.Context,
	wg *sync.WaitGroup, log logrus.FieldLogger, st *state.State,
	routes state.Table[*dptables.Route], customRoutes state.Table[*dptables.CustomRoute]) error {

	routesChanged :=
		stream.Trigger(
			ctx,
			stream.Filter(st.Observable,
				func(e state.Event) bool {
					return e.ForTable(routes.Name())
				}))

	customRoutesChanged :=
		stream.Trigger(
			ctx,
			stream.Filter(st.Observable,
				func(e state.Event) bool {
					return e.ForTable(customRoutes.Name())
				}))

	wg.Add(1)
	go func() {
		defer wg.Done()
		retryChan := make(chan error, 1)
		defer close(retryChan)

		for {
			err := reconcileRoutes(log, st, routes, customRoutes)
			if err != nil {
				log.WithError(err).Error("reconcileRoutes")
				retryChan <- err
			}

			select {
			case <-ctx.Done():
				return
			case <-routesChanged:
			case <-customRoutesChanged:
			case <-retryChan:
			}

			log.Info("Routes changed, reconciling.")

			// TODO proper throttling.
			time.Sleep(time.Second)

		}
	}()
	return nil
}

func reconcileRoutes(
	log logrus.FieldLogger,
	st *state.State,
	routes state.Table[*dptables.Route],
	customRoutes state.Table[*dptables.CustomRoute]) error {

	tx := st.Read()
	routeReadTx := routes.Read(tx)
	iter, err := customRoutes.Read(tx).Get(state.All)
	if err != nil {
		return err
	}

	// Reconcile the differences between the 'routes' table (reflecting actual system routing tables)
	// and 'custom-routes' (reflecting the desired additional routes).
	// This isn't really optimized in any way. Likely would like to update state of each custom route
	// to mark when it was created, whether it's failing etc.

	return state.ProcessEach(
		iter,
		func(cr *dptables.CustomRoute) error {
			if cr.Prefix == nil {
				// TODO
				return nil
			}
			// TODO this is just an example. Need to do deeper comparision.
			r, err := routeReadTx.First(dptables.ByPrefix(cr.Prefix))
			if err != nil {
				return err
			}
			if r == nil {
				// The custom route is missing from the system's routing table
				log.Infof("Adding missing route: %+v", cr)
				return addOrModifyRoute(cr)
			}
			return nil
		})

}

func addOrModifyRoute(cr *dptables.CustomRoute) error {
	// TODO just an example, probably wrong in some way.
	rt := netlink.Route{
		LinkIndex: cr.IfIndex,
		Dst:       cr.Prefix,
		Src:       cr.Local,
		MTU:       cr.MTU,
		Priority:  cr.Priority,
		Protocol:  netlink.RouteProtocol(cr.Proto),
		Table:     cr.Table,
		Type:      cr.Type,
		Gw:        cr.Nexthop,
		Scope:     cr.Scope,
	}
	return netlink.RouteReplace(&rt)
}

func parseLink(l netlink.Link) *dptables.Device {
	a := l.Attrs()
	return &dptables.Device{
		Name:  a.Name,
		Index: a.Index,
	}
}

func fillAddrs(l netlink.Link, d *dptables.Device) error {
	addrs, err := netlink.AddrList(l, netlink.FAMILY_ALL)
	if err != nil {
		return err
	}
	d.Addresses = make([]netip.Addr, len(addrs))
	for i := range addrs {
		// FIXME drop 'Must'
		d.Addresses[i] = netip.MustParseAddr(addrs[i].String())
	}
	return nil
}

func syncDevices(ctx context.Context, wg *sync.WaitGroup, log logrus.FieldLogger, st *state.State, devices state.Table[*dptables.Device]) error {
	linkChan := make(chan netlink.LinkUpdate)
	err := netlink.LinkSubscribe(linkChan, ctx.Done())
	if err != nil {
		return fmt.Errorf("LinkSubscribe: %w", err)
	}

	{
		initialLinks, err := netlink.LinkList()
		if err != nil {
			return fmt.Errorf("LinkList: %w", err)
		}
		tx := st.Write()
		devicesTx := devices.Modify(tx)
		for _, r := range initialLinks {
			devicesTx.Insert(parseLink(r))
		}
		tx.Commit()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for update := range linkChan {
			// TODO: batching/delaying.
			tx := st.Write()
			devicesTx := devices.Modify(tx)
			if update.Header.Type == unix.RTM_DELLINK {
				devicesTx.Delete(parseLink(update.Link))
			} else {
				d := parseLink(update.Link)
				if err := fillAddrs(update.Link, d); err != nil {
					log.WithError(err).Error("fillAddrs")
				}
				devicesTx.Insert(d)
			}
			tx.Commit()
		}
	}()
	return nil
}
