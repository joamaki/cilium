package lb

import (
	"context"
	"net"
	"sync"

	linuxDatapath "github.com/cilium/cilium/pkg/datapath/linux"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/stream"
	"github.com/vishvananda/netlink"
)

// TODO: This is just a quick-and-dirty wrapping of existing DeviceManager. Need to rewrite
// it to expose this API directly. This is also in the wrong package.
var DevicesCell = cell.Module(
	"datapath-devices",
	"Provides notifications on changed network devices",
	cell.Provide(newDevices),
)

type Device struct {
	Name string
	IPs  []net.IP
}

type Devices interface {
	stream.Observable[[]Device]
	Devices(ctx context.Context) <-chan []Device
}

type devices struct {
	stream.Observable[[]Device]
	k8sEnabled bool
	emit       func([]Device)
	complete   func(error)
	devs       chan []Device
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

func (d *devices) Devices(ctx context.Context) <-chan []Device {
	return stream.ToChannel(ctx, make(chan error), d.Observable)
}

func (d *devices) emitDevices(names []string) {
	devs := make([]Device, len(names))
	for i, name := range names {
		devs[i].Name = name
		link, err := netlink.LinkByName(name)
		if err == nil {
			addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
			if err == nil {
				for _, addr := range addrs {
					devs[i].IPs = append(devs[i].IPs, addr.IPNet.IP)
				}
			}
		}
	}
	d.emit(devs)
}

func (d *devices) Start(hive.HookContext) error {
	var ctx context.Context
	ctx, d.cancel = context.WithCancel(context.Background())

	dm, err := linuxDatapath.NewDeviceManager()
	if err != nil {
		d.complete(err)
		return err
	}

	initial, err := dm.Detect(d.k8sEnabled)
	if err != nil {
		d.complete(err)
		return err
	}

	go d.emitDevices(initial)

	devsChan, err := dm.Listen(ctx)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		for devs := range devsChan {
			d.emitDevices(devs)
		}
		d.complete(nil)
	}()

	return nil
}

func (d *devices) Stop(hive.HookContext) error {
	d.cancel()
	d.wg.Wait()
	return nil
}

func newDevices(lc hive.Lifecycle, c k8sClient.Clientset) (Devices, error) {
	// HACK HACK HACK
	option.Config.EnableNodePort = true
	// /HACK HACK HACK

	src, emit, complete := stream.Multicast[[]Device](stream.EmitLatest)
	d := &devices{k8sEnabled: c.IsEnabled(), Observable: src, emit: emit, complete: complete}
	lc.Append(d)
	return d, nil
}
