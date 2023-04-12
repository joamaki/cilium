// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package linux

import (
	"github.com/cilium/cilium/pkg/datapath/devices"
	"github.com/cilium/cilium/pkg/datapath/linux/config"
	"github.com/cilium/cilium/pkg/datapath/loader"
	datapath "github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/maps/lbmap"
)

// DatapathConfiguration is the static configuration of the datapath. The
// configuration cannot change throughout the lifetime of a datapath object.
type DatapathConfiguration struct {
	// HostDevice is the name of the device to be used to access the host.
	HostDevice string

	ProcFs string
}

type linuxDatapath struct {
	datapath.ConfigWriter
	datapath.IptablesManager
	nodeAddressing datapath.NodeAddressing
	config         DatapathConfiguration
	loader         *loader.Loader
	wgAgent        datapath.WireguardAgent
	lbmap          datapath.LBMap
}

type linuxDatapathParams struct {
	cell.In

	Config         DatapathConfiguration
	RuleManager    datapath.IptablesManager
	WGAgent        datapath.WireguardAgent
	NodeAddressing datapath.NodeAddressing
	Loader         *loader.Loader
	DeviceResolver devices.DeviceResolver
}

// NewDatapath creates a new Linux datapath
func NewDatapath(p linuxDatapathParams) datapath.Datapath {
	dp := &linuxDatapath{
		ConfigWriter:    &config.HeaderfileWriter{p.DeviceResolver},
		IptablesManager: p.RuleManager,
		nodeAddressing:  p.NodeAddressing,
		config:          p.Config,
		loader:          p.Loader,
		wgAgent:         p.WGAgent,
		lbmap:           lbmap.New(),
	}
	return dp
}

// LocalNodeAddressing returns the node addressing implementation of the local
// node
func (l *linuxDatapath) LocalNodeAddressing() datapath.NodeAddressing {
	return l.nodeAddressing
}

func (l *linuxDatapath) Loader() datapath.Loader {
	return l.loader
}

func (l *linuxDatapath) WireguardAgent() datapath.WireguardAgent {
	return l.wgAgent
}

func (l *linuxDatapath) Procfs() string {
	return l.config.ProcFs
}

func (l *linuxDatapath) LBMap() datapath.LBMap {
	return l.lbmap
}
