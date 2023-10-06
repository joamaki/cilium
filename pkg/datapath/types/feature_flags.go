package types

import "github.com/cilium/cilium/pkg/hive/cell"

type Flag struct {
	flag bool
}

func (f *Flag) Raise(v bool) {
	if v {
		f.flag = true
	}
}

func (f *Flag) Enabled() bool {
	return f.flag
}

// FeatureFlags is an immutable set of datapath feature flags composed
// by control-plane components during agent initialization.
type FeatureFlags struct {
	Tunneling           Flag
	EgressGatewayCommon Flag
}

// FeatureFlagsOption can be used to influence the datapath feature flags:
//
//	cell.Provide(func(myFeatureConfig Config) types.FeatureFlagsOut {
//	  return types.FeatureFlagsOut{
//	    Option: func(flags *types.FeatureFlags) {
//	      flags.TunnelingEnabled.Raise(myFeatureConfig.Enabled)
//	    },
//	  }
//	}
type FeatureFlagsOption func(*FeatureFlags)

// FeatureFlagsOut provides FeatureFlagsOption as group value which
// will be collected by NewFeatureFlags to compose the final FeatureFlags.
type FeatureFlagsOut struct {
	cell.Out

	Option FeatureFlagsOption `group:"feature-flags"`
}

type featureFlagsIn struct {
	cell.In

	Options []FeatureFlagsOption `group:"feature-flags"`
}

func NewFeatureFlags(in featureFlagsIn) (flags FeatureFlags) {
	for _, opt := range in.Options {
		opt(&flags)
	}
	return
}
