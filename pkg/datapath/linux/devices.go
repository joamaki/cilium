// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package linux

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/option"
)

// DeviceManager is a temporary compatibility bridge to keep DeviceManager uses as is and reuse its tests
// against DevicesController and the devices table.
//
// This will be refactored away in follow-up PRs that convert code over to the devices table.
// The DirectRoutingDevice and IPv6MCastDevice would computed from the devices table as necessary.
type DeviceManager struct {
	params devicesControllerParams
	hive   *hive.Hive
}

func (dm *DeviceManager) Detect(k8sEnabled bool) ([]string, error) {
	hasWildcard := false
	userDevices := option.Config.GetDevices()
	for _, name := range userDevices {
		hasWildcard = hasWildcard || strings.HasSuffix(name, "+")
	}

	devs, _ := tables.ViableDevices(dm.params.DeviceTable.Reader(dm.params.DB.ReadTxn()))
	names := tables.DeviceNames(devs)

	if len(names) == 0 && hasWildcard {
		// Fail if user provided a device wildcard which didn't match anything.
		return nil, fmt.Errorf("No device found matching %v", userDevices)
	}

	option.Config.SetDevices(names)

	var nodeDevice *tables.Device
	if k8sEnabled {
		nodeIP := node.GetK8sNodeIP()
		for _, dev := range devs {
			if dev.HasIP(nodeIP) {
				nodeDevice = dev
				break
			}
		}
	}
	if option.Config.DirectRoutingDeviceRequired() {
		var filter deviceFilter
		if option.Config.DirectRoutingDevice != "" {
			filter = deviceFilter(strings.Split(option.Config.DirectRoutingDevice, ","))
		}
		option.Config.DirectRoutingDevice = ""
		if len(devs) == 1 {
			option.Config.DirectRoutingDevice = devs[0].Name
		} else if nodeDevice != nil && filter.match(nodeDevice.Name) {
			option.Config.DirectRoutingDevice = nodeDevice.Name
		} else {
			return nil, fmt.Errorf("unable to determine direct routing device. Use --%s to specify it",
				option.DirectRoutingDevice)
		}
	}
	if option.Config.EnableIPv6NDP && option.Config.IPv6MCastDevice == "" {
		if nodeDevice != nil && nodeDevice.Link.Attrs().Flags&net.FlagMulticast != 0 {
			option.Config.IPv6MCastDevice = nodeDevice.Name
		} else {
			return nil, fmt.Errorf("unable to determine Multicast device. Use --%s to specify it",
				option.IPv6MCastDevice)
		}
	}

	return names, nil
}

func (dm *DeviceManager) Listen(ctx context.Context) (chan []string, error) {
	devs := make(chan []string)
	go func() {
		for {
			iter, invalidated := tables.ViableDevices(dm.params.DeviceTable.Reader(dm.params.DB.ReadTxn()))
			devs <- tables.DeviceNames(iter)
			select {
			case <-invalidated:
			case <-ctx.Done():
				close(devs)
				return
			}
		}
	}()
	return devs, nil
}

func newDeviceManager(p devicesControllerParams) *DeviceManager {
	return &DeviceManager{params: p, hive: nil}
}

func (dm *DeviceManager) Stop() {
	if dm.hive != nil {
		dm.hive.Stop(context.TODO())
	}
}
