package devices

import (
	"context"
	"fmt"
	"sync"

	"github.com/cilium/cilium/lbtest/datapath/api"
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
var Cell = cell.Module(
	"datapath-devices",
	"Provides notifications on changed network devices",
	cell.Provide(New),
)

type devices struct {
	stream.Observable[[]api.Device]
	k8sEnabled bool
	emit       func([]api.Device)
	complete   func(error)
	devs       chan []api.Device
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	reporter   cell.StatusReporter

	testConfig *TestConfig
}

func (d *devices) Devices(ctx context.Context) <-chan []api.Device {
	return stream.ToChannel(ctx, make(chan error), d.Observable)
}

func (d *devices) emitDevices(names []string) {
	devs := make([]api.Device, len(names))
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
	d.reporter.OK(fmt.Sprintf("Detected devices: %v", names))
}

func (d *devices) Start(hive.HookContext) error {
	if d.testConfig != nil {
		// TODO: Not great way of doing this. Perhaps consider allowing decoration
		// into inside modules, e.g. we could take datapath cell and swap out
		// devices from it. Though that's not that great either... :(
		d.emit(d.testConfig.TestDevices)
		return nil
	}

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

type TestConfig struct {
	TestDevices []api.Device
}

type devicesParams struct {
	cell.In

	TestConfig     *TestConfig `optional:"true"`
	Lifecycle      hive.Lifecycle
	Clientset      k8sClient.Clientset `optional:"true"`
	StatusReporter cell.StatusReporter
}

func New(p devicesParams) (api.Devices, error) {
	// HACK HACK HACK
	option.Config.EnableNodePort = true
	option.Config.EnableIPv4 = true
	option.Config.EnableIPv6 = true
	// /HACK HACK HACK

	src, emit, complete := stream.Multicast[[]api.Device](stream.EmitLatest)
	k8sEnabled := p.Clientset != nil && p.Clientset.IsEnabled()
	d := &devices{k8sEnabled: k8sEnabled, Observable: src, emit: emit, complete: complete, reporter: p.StatusReporter, testConfig: p.TestConfig}
	p.Lifecycle.Append(d)
	return d, nil
}
