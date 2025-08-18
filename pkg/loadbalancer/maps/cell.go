// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package maps

import (
	"errors"
	"sync"

	"github.com/cilium/hive/cell"
	"golang.org/x/sys/unix"

	"github.com/cilium/cilium/pkg/netns"
)

// Provides [LBMap] a wrapper around the load-balancing BPF maps
var Cell = cell.Module(
	"loadbalancer-maps",
	"Load-balancing BPF maps",

	cell.Provide(
		// [LBMaps], abstraction for the load-balancing BPF map access.
		newLBMaps,

		// 'lb/' script commands for debugging and testing.
		scriptCommands,

		// [HaveNetNSCookieSupport] to probe for netns cookie support.
		NetnsCookieSupportFunc,

		// [Restored] for access to previous contents of the LB maps.
		NewRestored,
	),

	// Register a periodic job to update the BPF map pressure metrics.
	cell.Invoke(registerPressureMetricsReporter),
)

type HaveNetNSCookieSupport func() bool

func NetnsCookieSupportFunc() HaveNetNSCookieSupport {
	return sync.OnceValue(func() bool {
		_, err := netns.GetNetNSCookie()
		return !errors.Is(err, unix.ENOPROTOOPT)
	})
}
