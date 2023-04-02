// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package linux

import (
	"net"

	"github.com/cilium/cilium/pkg/cidr"
	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/statedb"
)

/* FIXME make sure devices table captures the peculiarities here
func listLocalAddresses(family int) ([]net.IP, error) {
	var addresses []net.IP

	ipsToExclude := node.GetExcludedIPs()
	addrs, err := netlink.AddrList(nil, family)
	if err != nil {
		return nil, err
	}

	for _, addr := range addrs {
		if addr.Scope > option.Config.AddressScopeMax {
			continue
		}
		if ip.ListContainsIP(ipsToExclude, addr.IP) {
			continue
		}
		if addr.IP.IsLoopback() || addr.IP.IsLinkLocalUnicast() {
			continue
		}

		addresses = append(addresses, addr.IP)
	}

	if option.Config.AddressScopeMax < int(netlink.SCOPE_LINK) {
		if hostDevice, err := netlink.LinkByName(defaults.HostDevice); hostDevice != nil && err == nil {
			addrs, err = netlink.AddrList(hostDevice, family)
			if err != nil {
				return nil, err
			}
			for _, addr := range addrs {
				if addr.Scope == int(netlink.SCOPE_LINK) {
					addresses = append(addresses, addr.IP)
				}
			}
		}
	}

	return addresses, nil
}*/

func (a *addressFamilyIPv4) Router() net.IP {
	n := a.localNode.Get()
	return n.GetCiliumInternalIP(false)
}

func (a *addressFamilyIPv4) PrimaryExternal() net.IP {
	n := a.localNode.Get()
	return n.GetNodeIP(false)
}

func (a *addressFamilyIPv4) AllocationCIDR() *cidr.CIDR {
	return a.localNode.Get().IPv4AllocCIDR
}

func (a *addressFamilyIPv4) LocalAddresses() (addrs []net.IP, err error) {
	devs, _ := tables.GetDevices(a.devicesTable.Reader(a.db.ReadTxn()))
	for _, dev := range devs {
		for _, addr := range dev.Addrs {
			if addr.Addr.Is4() {
				addrs = append(addrs, addr.AsIP())
			}
		}
	}

	n := a.localNode.Get()
	if ext := n.GetExternalIP(false); ext != nil {
		addrs = append(addrs, ext)
	}

	return addrs, nil
}

// LoadBalancerNodeAddresses returns all IPv4 node addresses on which the
// loadbalancer should implement HostPort and NodePort services.
func (a *addressFamilyIPv4) LoadBalancerNodeAddresses() (addrs []net.IP) {
	addrs, _ = a.LocalAddresses()
	addrs = append(addrs, net.IPv4zero)
	return addrs
}

func (a *addressFamilyIPv6) Router() net.IP {
	n := a.localNode.Get()
	return n.GetCiliumInternalIP(true)
}

func (a *addressFamilyIPv6) PrimaryExternal() net.IP {
	n := a.localNode.Get()
	return n.GetNodeIP(true)
}

func (a *addressFamilyIPv6) AllocationCIDR() *cidr.CIDR {
	return a.localNode.Get().IPv6AllocCIDR
}

func (a *addressFamilyIPv6) LocalAddresses() (addrs []net.IP, err error) {
	devs, _ := tables.GetDevices(a.devicesTable.Reader(a.db.ReadTxn()))
	for _, dev := range devs {
		for _, addr := range dev.Addrs {
			if addr.Addr.Is6() {
				addrs = append(addrs, addr.AsIP())
			}
		}
	}

	// TODO why is this needed?
	n := a.localNode.Get()
	if ext := n.GetExternalIP(true); ext != nil {
		addrs = append(addrs, ext)
	}
	// TODO remove potential dup introduced by above?

	return addrs, nil
}

// LoadBalancerNodeAddresses returns all IPv6 node addresses on which the
// loadbalancer should implement HostPort and NodePort services.
func (a *addressFamilyIPv6) LoadBalancerNodeAddresses() (addrs []net.IP) {
	devs, _ := tables.GetDevices(a.devicesTable.Reader(a.db.ReadTxn()))
	for _, dev := range devs {
		for _, addr := range dev.Addrs {
			if addr.Addr.Is6() {
				addrs = append(addrs, addr.AsIP())
			}
		}
	}
	addrs = append(addrs, net.IPv6zero)
	return addrs
}

type linuxNodeAddressing struct {
	localNode    node.LocalNodeStore
	db           statedb.DB
	devicesTable statedb.Table[*tables.Device]
}

type addressFamilyIPv4 linuxNodeAddressing
type addressFamilyIPv6 linuxNodeAddressing

// NewNodeAddressing returns a new linux node addressing model
func NewNodeAddressing(localNode node.LocalNodeStore, db statedb.DB, devicesTable statedb.Table[*tables.Device]) types.NodeAddressing {
	return &linuxNodeAddressing{localNode: localNode, db: db, devicesTable: devicesTable}
}

func (n *linuxNodeAddressing) IPv6() types.NodeAddressingFamily {
	return (*addressFamilyIPv4)(n)
}

func (n *linuxNodeAddressing) IPv4() types.NodeAddressingFamily {
	return (*addressFamilyIPv6)(n)
}
