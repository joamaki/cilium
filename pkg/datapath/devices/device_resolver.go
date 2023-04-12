package devices

import (
	"net"

	"github.com/spf13/pflag"

	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/statedb"
)

// DeviceResolverCell provides the DeviceResolver.
var DeviceResolverCell = cell.Provide(newDeviceResolver)

// DeviceResolver finds the direct routing and multicast devices
// from the set of all viable devices.
type DeviceResolver interface {
	DirectRoutingDevice() *tables.Device
	IPv6MCastDevice() *tables.Device
	NodePortAddrs() map[string]tables.NodePortAddr
	Devices() []*tables.Device
}

type deviceResolverConfig struct {
	// DirectRoutingDevice is the name of a device used to connect nodes in
	// direct routing mode (only required by BPF NodePort)
	DirectRoutingDevice string

	// IPv6MCastDevice is the name of device that joins IPv6's solicitation multicast group
	IPv6MCastDevice string
}

func (def deviceResolverConfig) Flags(flags *pflag.FlagSet) {
	flags.String(option.DirectRoutingDevice, "", "Device name used to connect nodes in direct routing mode (used by BPF NodePort, BPF host routing; if empty, automatically set to a device with k8s InternalIP/ExternalIP or with a default route)")
	flags.String(option.IPv6MCastDevice, "", "Device that joins a Solicited-Node multicast group for IPv6")
}

type deviceResolverParams struct {
	Config    deviceResolverConfig
	DB        statedb.DB
	Table     statedb.Table[*tables.Device]
	LocalNode node.LocalNodeStore
}

type deviceResolver struct {
	deviceResolverParams
}

func newDeviceResolver(p deviceResolverParams) DeviceResolver {
	return &deviceResolver{p}
}

func (d *deviceResolver) Devices() []*tables.Device {
	r := d.Table.Reader(d.DB.ReadTxn())
	devs, _ := tables.ViableDevices(r)
	return devs
}

func (d *deviceResolver) DirectRoutingDevice() *tables.Device {
	r := d.Table.Reader(d.DB.ReadTxn())

	if d.Config.DirectRoutingDevice != "" {
		dev, _ := r.First(tables.DeviceByName(d.Config.DirectRoutingDevice))
		return dev
	}

	// TODO: Support watching changes to the direct routing device?
	// e.g. to reconfigure when node IP changes.
	devs, _ := tables.ViableDevices(r)

	// If only one device exist, use that.
	if len(devs) == 1 {
		return devs[0]
	}

	// Otherwise find the device that has the Node IP on it.
	localNode := d.LocalNode.Get()
	nodeIP := localNode.GetK8sNodeIP()
	for _, dev := range devs {
		for _, addr := range dev.Addrs {
			if addr.AsIP().Equal(nodeIP) {
				return dev
			}
		}
	}
	return nil
}

func (d *deviceResolver) IPv6MCastDevice() *tables.Device {
	r := d.Table.Reader(d.DB.ReadTxn())
	if d.Config.IPv6MCastDevice != "" {
		dev, _ := r.First(tables.DeviceByName(d.Config.IPv6MCastDevice))
		return dev
	}

	// Fall back to the direct routing device. If that is not specified
	// by the user, then this will pick the device with the node IP.
	dev := d.DirectRoutingDevice()
	if dev.Link.Attrs().Flags&net.FlagMulticast != 0 {
		return dev
	}
	return nil
}

func (d *deviceResolver) NodePortAddrs() map[string]tables.NodePortAddr {
	addrs := map[string]tables.NodePortAddr{}
	for _, dev := range d.Devices() {
		if len(dev.Addrs) == 0 {
			continue
		}
		addrs[dev.Name] = dev.NodePortAddr()
	}
	return addrs
}
