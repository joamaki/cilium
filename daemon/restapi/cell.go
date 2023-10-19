// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package restapi

import (
	"github.com/cilium/cilium/api/v1/server"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var Cell = cell.Module(
	"cilium-restapi",
	"Cilium Agent API",

	server.APICell,  // CiliumAPIAPI
	rateLimiterCell, // Request rate-limiting
)
