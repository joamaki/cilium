// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package lbmap

import (
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
)

const DefaultMaxEntries = 65536

var log = logging.DefaultLogger.WithField(logfields.LogSubsys, "map-lb")

var (
	// MaxEntries contains the maximum number of entries that are allowed
	// in Cilium LB service, backend and affinity maps.
	ServiceMapMaxEntries        = DefaultMaxEntries
	ServiceBackEndMapMaxEntries = DefaultMaxEntries
	RevNatMapMaxEntries         = DefaultMaxEntries
	AffinityMapMaxEntries       = DefaultMaxEntries
	SourceRangeMapMaxEntries    = DefaultMaxEntries
	MaglevMapMaxEntries         = DefaultMaxEntries
)
