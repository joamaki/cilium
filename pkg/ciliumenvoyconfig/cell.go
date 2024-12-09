// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package ciliumenvoyconfig

import (
	"github.com/cilium/hive/cell"
	"github.com/spf13/pflag"

	"github.com/cilium/cilium/pkg/proxy"
	"github.com/cilium/cilium/pkg/time"
)

// Cell provides support for the CRD CiliumEnvoyConfig that backs Ingress, Gateway API
// and L7 loadbalancing.
var Cell = cell.Module(
	"ciliumenvoyconfig",
	"CiliumEnvoyConfig",

	cell.Config(cecConfig{}),
	cell.ProvidePrivate(
		newPortAllocator,
		newCECResourceParser,
	),
	experimentalCell,
)

type cecConfig struct {
	EnvoyConfigRetryInterval  time.Duration
	EnvoyConfigTimeout        time.Duration
	ProxyMaxConcurrentRetries uint32
}

func (r cecConfig) Flags(flags *pflag.FlagSet) {
	flags.Duration("envoy-config-retry-interval", 15*time.Second, "Interval in which an attempt is made to reconcile failed EnvoyConfigs. If the duration is zero, the retry is deactivated.")
	flags.Duration("envoy-config-timeout", 2*time.Minute, "Timeout that determines how long to wait for Envoy to N/ACK CiliumEnvoyConfig resources")
	flags.Uint32("proxy-max-concurrent-retries", 128, "Maximum number of concurrent retries on Envoy clusters")
}

func newPortAllocator(proxy *proxy.Proxy) PortAllocator {
	return proxy
}
