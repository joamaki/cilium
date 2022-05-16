// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package service

import "go.uber.org/fx"

var Module = fx.Module(
	"service",

	//fx.Provide(NewService),

	// API handlers
	fx.Provide(
		NewGetServiceIDHandler,
		NewPutServiceIDHandler,
		NewDeleteServiceIDHandler,
		NewGetServiceHandler,
	),
)
