package mtu

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/stream"
)

var Cell = cell.Module(
	"mtu",
	"Computes MTU from network devices",
	cell.Provide(New),
)

// mtuFromDevices implements MTU discovery by deriving it from the
// system network devices and routes.
type mtuFromDevices struct {
	log     logrus.FieldLogger
	db      statedb.DB
	devices statedb.Table[*tables.Device]
	routes  statedb.Table[*tables.Route]

	stream.Observable[int]
	emit     func(int)
	complete func(error)
}

const (
	DefaultMTU = 1500
)

func (m *mtuFromDevices) reconcile(ctx context.Context) error {
	defer m.complete(nil)

	emit := func(mtu int, source string) {
		m.log.WithField("mtu", mtu).Infof("Detected MTU from %s", source)
		m.emit(mtu)
	}

	// FIXME if we have a NodeIP then use the MTU of that device.
	// What's a clean way to get that information here? Not sure about
	// depending on LocalNodeStore directly (e.g. somewhat against the
	// idea of pkg/datapath being the bottom layer of the stack). OTOH
	// "LocalNodeStore" is pretty fundamental and it's already somewhat
	// decoupled from both "control-plane" and "datapath".

	for ctx.Err() == nil {
		txn := m.db.ReadTxn()

		routeChanged, defaultRoute, err :=
			m.routes.Reader(txn).FirstWatch(tables.RouteByDst(nil))
		if err != nil {
			panic("Impossible: RouteByDst is broken: " + err.Error())
		}
		if defaultRoute == nil {
			emit(DefaultMTU, "defaults (no default route exists)")
			select {
			case <-routeChanged:
			case <-ctx.Done():
			}
			continue
		}

		deviceChanged, device, err := m.devices.Reader(txn).FirstWatch(tables.DeviceByIndex(defaultRoute.LinkIndex))
		if err != nil {
			panic("Impossible: DeviceByIndex is broken: " + err.Error())
		}

		if device != nil {
			emit(device.MTU, "device "+device.Name)
		} else {
			// Route exists, but there's no device (either not observed yet
			// or it was just removed). Wait for the device to either appear
			// or the default route to change.
		}

		// Wait for the default route or the device to change
		select {
		case <-ctx.Done():
		case <-routeChanged:
		case <-deviceChanged:
		}
	}
	return nil

}

// Get implements types.MTU
func (*mtuFromDevices) Get() int {
	panic("unimplemented")
}

var _ types.MTU = &mtuFromDevices{}

type params struct {
	cell.In

	Lifecycle hive.Lifecycle
	Log       logrus.FieldLogger
	Registry  job.Registry
	DB        statedb.DB
	Devices   statedb.Table[*tables.Device]
	Routes    statedb.Table[*tables.Route]
}

func New(p params) types.MTU {
	m := &mtuFromDevices{log: p.Log, db: p.DB, devices: p.Devices, routes: p.Routes}
	m.Observable, m.emit, m.complete = stream.Multicast[int](stream.EmitLatest)
	jobs := p.Registry.NewGroup()
	jobs.Add(job.OneShot(
		"mtu-reconcile",
		m.reconcile))
	p.Lifecycle.Append(jobs)
	return m
}
