// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package linux

import (
	"github.com/cilium/cilium/pkg/datapath"
	"github.com/cilium/cilium/pkg/datapath/linux/config"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/maps/lbmap"
	"github.com/cilium/cilium/pkg/option"
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
	node           datapath.NodeHandler
	nodeAddressing types.NodeAddressing
	config         DatapathConfiguration
	loader         datapath.Loader
	wgAgent        datapath.WireguardAgent
	lbmap          types.LBMap
}

// NewDatapath creates a new Linux datapath
func NewDatapath(loader datapath.Loader, ruleManager datapath.IptablesManager, wgAgent datapath.WireguardAgent) datapath.Datapath {
	cfg := DatapathConfiguration{
		HostDevice: defaults.HostDevice,
		ProcFs:     option.Config.ProcFs,
	}

	dp := &linuxDatapath{
		ConfigWriter:    &config.HeaderfileWriter{},
		IptablesManager: ruleManager,
		nodeAddressing:  NewNodeAddressing(),
		config:          cfg,
		loader:          loader,
		wgAgent:         wgAgent,
		lbmap:           lbmap.New(),
	}

	dp.node = NewNodeHandler(cfg, dp.nodeAddressing, wgAgent)
	return dp
}

// Node returns the handler for node events
func (l *linuxDatapath) Node() datapath.NodeHandler {
	return l.node
}

// LocalNodeAddressing returns the node addressing implementation of the local
// node
func (l *linuxDatapath) LocalNodeAddressing() types.NodeAddressing {
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

func (l *linuxDatapath) LBMap() types.LBMap {
	return l.lbmap
}
