// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package recorder

import "go.uber.org/fx"

var Module = fx.Module(
	"recorder",

	fx.Provide(
		NewRecorder,
	),
)
