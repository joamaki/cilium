package cell

import (
	upstream "github.com/cilium/hive/cell"

	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
)

var (
	log = logging.DefaultLogger.WithField(logfields.LogSubsys, "hive")
)

type (
	Lifecycle        = upstream.Lifecycle
	DefaultLifecycle = upstream.DefaultLifecycle
	Cell             = upstream.Cell
	Hook             = upstream.Hook
	HookInterface    = upstream.HookInterface
	HookContext      = upstream.HookContext

	Scope          = upstream.Health
	Health         = upstream.Health
	HealthReporter = upstream.Health

	AllSettings  = upstream.AllSettings
	FullModuleID = upstream.FullModuleID
	ModuleID     = upstream.ModuleID
	Level        = upstream.Level

	In  = upstream.In
	Out = upstream.Out
)

var (
	Provide        = upstream.Provide
	ProvidePrivate = upstream.ProvidePrivate
	Invoke         = upstream.Invoke
	Module         = upstream.Module
	Group          = upstream.Group
	Decorate       = upstream.Decorate

	StatusUnknown  = upstream.StatusUnknown
	StatusStopped  = upstream.StatusStopped
	StatusDegraded = upstream.StatusDegraded
	StatusOK       = upstream.StatusOK
)

func Config[T upstream.Flagger](cfg T) upstream.Cell {
	return upstream.Config[T](cfg)
}

func GetHealthReporter(scope Scope, name string) HealthReporter {
	return scope.NewScope(name)
}

func GetSubScope(parent Scope, name string) Scope {
	return parent.NewScope(name)
}

func TestScope() Scope {
	_, h := upstream.NewSimpleHealth()
	return h
}
