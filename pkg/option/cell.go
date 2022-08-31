package option

import (
	"path/filepath"

	"github.com/spf13/pflag"
	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/hive"
)

// BaseConfig contains common configuration options used by many
// cells. These should be truly common.
type BaseConfig struct {
	LibDir   string // Cilium library files directory
	StateDir string // StateDir is the directory where runtime state of endpoints is stored

	// Cilium runtime directory (by default same as StateDir)
	RunDir string `mapstructure:"state-dir"`

	ConfigFile string `mapstructure:"config"`
	ConfigDir  string
	Debug      bool
}

// BPF template files directory
func (b *BaseConfig) BpfDir() string {
	return filepath.Join(b.LibDir, defaults.BpfDir)
}

func (BaseConfig) CellFlags(flags *pflag.FlagSet) {
	flags.String(LibDir, defaults.LibraryPath, "Directory path to store runtime build environment")
	flags.String(StateDir, defaults.RuntimePath, "Directory path to store runtime state")
	flags.String(ConfigFile, "", `Configuration file (default "$HOME/ciliumd.yaml")`)
	flags.String(ConfigDir, "", `Configuration directory that contains a file for each option`)
	flags.BoolP(DebugArg, "D", false, "Enable debugging mode")
}

var BaseConfigCell = hive.NewCellWithConfig[BaseConfig](
	"base-config",
	fx.Invoke(populateBaseConfig),
)

func populateBaseConfig(baseCfg BaseConfig) {
	// Fill in BaseConfig into DaemonConfig. This happens before
	// runDaemon/initEnv. This can be removed once we've moved away
	// from DaemonConfig.
	Config.BaseConfig = baseCfg
}
