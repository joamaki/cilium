package cmd

import (
	"github.com/cilium/cilium/pkg/cidr"
	"github.com/cilium/cilium/pkg/datapath"
	ipamOption "github.com/cilium/cilium/pkg/ipam/option"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/mtu"
	"github.com/cilium/cilium/pkg/option"
)

func enableLocalNodeRoute() bool {
	return option.Config.EnableLocalNodeRoute &&
		option.Config.IPAM != ipamOption.IPAMENI &&
		option.Config.IPAM != ipamOption.IPAMAzure &&
		option.Config.IPAM != ipamOption.IPAMAlibabaCloud
}

func newLocalNodeConfiguration(mtuConfig mtu.Configuration) datapath.LocalNodeConfiguration {
	auxPrefixes := []*cidr.CIDR{}

	if option.Config.IPv4ServiceRange != AutoCIDR {
		serviceCIDR, err := cidr.ParseCIDR(option.Config.IPv4ServiceRange)
		if err != nil {
			log.WithError(err).WithField(logfields.V4Prefix, option.Config.IPv4ServiceRange).Fatal("Invalid IPv4 service prefix")
		}

		auxPrefixes = append(auxPrefixes, serviceCIDR)
	}

	if option.Config.IPv6ServiceRange != AutoCIDR {
		serviceCIDR, err := cidr.ParseCIDR(option.Config.IPv6ServiceRange)
		if err != nil {
			log.WithError(err).WithField(logfields.V6Prefix, option.Config.IPv6ServiceRange).Fatal("Invalid IPv6 service prefix")
		}

		auxPrefixes = append(auxPrefixes, serviceCIDR)
	}

	return datapath.LocalNodeConfiguration{
		MtuConfig:               mtuConfig,
		UseSingleClusterRoute:   option.Config.UseSingleClusterRoute,
		EnableIPv4:              option.Config.EnableIPv4,
		EnableIPv6:              option.Config.EnableIPv6,
		EnableEncapsulation:     option.Config.Tunnel != option.TunnelDisabled,
		EnableAutoDirectRouting: option.Config.EnableAutoDirectRouting,
		EnableLocalNodeRoute:    enableLocalNodeRoute(),
		AuxiliaryPrefixes:       auxPrefixes,
		EnableIPSec:             option.Config.EnableIPSec,
		EncryptNode:             option.Config.EncryptNode,
		IPv4PodSubnets:          option.Config.IPv4PodSubnets,
		IPv6PodSubnets:          option.Config.IPv6PodSubnets,
	}
}
