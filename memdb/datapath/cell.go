package datapath

import (
	"fmt"
	"net"
	"time"

	"github.com/cilium/cilium/memdb/datapath/controllers"
	"github.com/cilium/cilium/memdb/datapath/datasources"
	"github.com/cilium/cilium/memdb/datapath/maps"
	"github.com/cilium/cilium/memdb/datapath/tables"
	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var Cell = cell.Module(
	"datapath",
	"Datapath",

	cell.ProvidePrivate(func() *maps.ServiceMap { return &maps.ServiceMap{} }),
	controllers.Cell,
	tables.Cell,
	datasources.Cell,

	cell.Invoke(addCustomRoute),
)

// Add a custom route to test the reconciling of routes in the netlink datasource.
func addCustomRoute(st *state.State, devices state.Table[*tables.Device], customRoutes state.Table[*tables.CustomRoute]) {
	go func() {
		time.Sleep(time.Second) // XXX hack to wait for devices to populate. Do not do this.

		tx := st.Write()

		// Find the dummy0 device as we need the ifindex for the route.
		dev, _ := devices.Read(tx).First(tables.ByName("dummy0"))
		if dev == nil {
			fmt.Printf("No dummy0, not adding custom route.\n")
			return
		}

		_, ipnet, err := net.ParseCIDR("1.2.3.0/24")
		if err != nil {
			fmt.Printf("ParseCIDR: %s\n", err)
			return
		}

		cr := &tables.CustomRoute{
			Revision: 0,
			Prefix:   ipnet,
			IfIndex:  dev.Index,
			Table:    0,
		}

		err = customRoutes.Modify(tx).Insert(cr)
		if err != nil {
			fmt.Printf("addCustomRoute failed: %q\n", err)
			panic(err)
		}
		tx.Commit()
	}()
}
