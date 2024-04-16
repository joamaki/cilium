package hive

import (
	"fmt"
	"reflect"
	"time"

	upstream "github.com/cilium/hive"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"

	"github.com/cilium/cilium/pkg/cidr"
	"github.com/cilium/cilium/pkg/healthv2"
	healthTypes "github.com/cilium/cilium/pkg/healthv2/types"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/reconciler"

	"github.com/cilium/hive/cell"
)

type (
	Hive       = upstream.Hive
	Options    = upstream.Options
	Shutdowner = upstream.Shutdowner
)

var (
	ShutdownWithError = upstream.ShutdownWithError
)

type moduleDecoratorIn struct {
	cell.In

	ModuleID       cell.ModuleID
	FullModuleID   cell.FullModuleID
	Log            logrus.FieldLogger
	HealthProvider healthTypes.Provider
}

type moduleDecoratorOut struct {
	cell.Out

	Log    logrus.FieldLogger
	Health cell.Health
}

func moduleDecorator(in moduleDecoratorIn) moduleDecoratorOut {
	fmt.Printf("moduleDecorator %s, %s\n", in.ModuleID, in.FullModuleID)
	return moduleDecoratorOut{
		Log:    in.Log.WithField(logfields.LogSubsys, in.ModuleID),
		Health: in.HealthProvider.ForModule(in.FullModuleID),
	}
}

// New wraps the hive.New to create a hive with defaults used by cilium-agent.
// pkg/hive should eventually go away and this code should live in e.g. daemon/cmd
// or operator/cmd.
func New(cells ...cell.Cell) *Hive {
	cells = append(
		slices.Clone(cells),

		healthv2.Cell,
		job.Cell,
		statedb.Cell,
		reconciler.Cell,

		cell.Provide(
			func() logrus.FieldLogger { return logging.DefaultLogger },
			func(provider healthTypes.Provider) cell.Health {
				return provider.ForModule(nil)
			},
		))
	return upstream.NewWithOptions(
		upstream.Options{
			Logger:          slogTextLogger,
			EnvPrefix:       "CILIUM_",
			ModuleDecorator: moduleDecorator,
			DecodeHooks:     decodeHooks,
			StartTimeout:    10 * time.Minute,
			StopTimeout:     10 * time.Minute,
		},
		cells...,
	)
}

var decodeHooks = cell.DecodeHooks{
	// Decode *cidr.CIDR fields
	func(from reflect.Type, to reflect.Type, data interface{}) (interface{}, error) {
		if from.Kind() != reflect.String {
			return data, nil
		}
		s := data.(string)
		if to != reflect.TypeOf((*cidr.CIDR)(nil)) {
			return data, nil
		}
		return cidr.ParseCIDR(s)
	},
}

func AddConfigOverride[Cfg cell.Flagger](h *Hive, override func(*Cfg)) {
	upstream.AddConfigOverride[Cfg](h, override)
}
