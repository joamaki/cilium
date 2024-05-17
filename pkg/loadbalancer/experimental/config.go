// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import "github.com/spf13/pflag"

type Config struct {
	EnableNewServices bool
}

func (def Config) Flags(flags *pflag.FlagSet) {
	flags.Bool("enable-new-services", def.EnableNewServices, "Enable use of the new service load-balancing API")
	flags.MarkHidden("enable-new-services")
}

var DefaultConfig = Config{
	EnableNewServices: false,
}
