// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package linux

import (
	"context"

	"github.com/joamaki/goreactive/stream"
	"github.com/vishvananda/netlink"
	"go.uber.org/fx"
)

// Experiment to wrap the DeviceManager into a module and expose device information
// as a stream.

var DevicesModule = fx.Module(
	"datapath-devices",

	fx.Provide(NewDeviceManager, newDevicesObservable),
)

type Device struct {
	Name string
	Addresses []netlink.Addr
}

type Devices []Device

type DevicesObservable struct { stream.Observable[Devices] }

func resolveDevices(names []string) Devices {
	// TODO: How to handle errors here? stream.Retry?

	var devs Devices
	for _, name := range names {
		if link, err := netlink.LinkByName(name); err == nil {
			if addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL); err == nil {
				devs = append(devs, Device{Name: name, Addresses: addrs})
			}
		}
	}
	return devs
}

func startDeviceManager(ctx context.Context, dm *DeviceManager, startObservable func(stream.Observable[Devices])) error {
	updates, err := dm.Listen(ctx)
	if err != nil { return err }

	src, connect :=	stream.Multicast(stream.MulticastParams{BufferSize: 1, EmitLatest: true},
			stream.Map(stream.FromChannel(updates), resolveDevices))

	// Start observing the updates.
	// TODO: Catch and log the error? Retry? Currently no errors from upstream through.
	go connect(ctx)

	// And provide the now connected observable to subscribers.
	startObservable(src)

	return nil
}

func newDevicesObservable(lc fx.Lifecycle, dm *DeviceManager) DevicesObservable {
	ctx, cancel := context.WithCancel(context.Background())

	// TODO: Multicast is a cold observable, so we could skip using Deferred by
	// using stream.FromFunction on Listen+FromChannel, but then we can't bubble up
	// the error from Listen() in the OnStart hook. Might be OK though?
	devs, start := stream.Deferred[Devices]()

	lc.Append(fx.Hook{
		  OnStart: func(context.Context) error {
			  return startDeviceManager(ctx, dm, start)
		  },
		  OnStop: func(context.Context) error {
			  cancel()
			  return nil
		  },
	})
	return DevicesObservable{devs}
}
