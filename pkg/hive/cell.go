// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive

import (
	"context"
	"reflect"

	"github.com/spf13/pflag"
	"go.uber.org/fx"
)

// CellConfig is implemented by configuration structs to provide configuration
// for a cell.
type CellConfig interface {
	// CellFlags registers the configuration options as command-line flags.
	//
	// By convention a flag name matches the field name
	// if they're the same under case-insensitive comparison when dashes are
	// removed. E.g. "my-config-flag" matches field "MyConfigFlag". The
	// correspondence to the flag can be also specified with the mapstructure
	// tag: MyConfigFlag `mapstructure:"my-config-flag"`.
	//
	// Exported fields that are not found from the viper settings will cause
	// hive.Run() to fail. Unexported fields are ignored.
	//
	// See https://pkg.go.dev/github.com/mitchellh/mapstructure for more info.
	CellFlags(*pflag.FlagSet)
}

// Cell is a modular component of the hive, consisting of configuration and
// a set of constructors and objects (as fx.Options).
type Cell struct {
	newConfig     func() any
	registerFlags func(*pflag.FlagSet)
	name          string
	opts          []fx.Option
	flags         []string // Flags registered for the cell. Populated after call to registerFlags().
}

// NewCell constructs a new cell with the given name and options.
func NewCell(name string, opts ...fx.Option) *Cell {
	return &Cell{
		name:          name,
		opts:          opts,
		registerFlags: func(*pflag.FlagSet) {},
		newConfig:     nil,
	}
}

// Invoke constructs an unnamed cell for an invoke function.
func Invoke(fn any) *Cell {
	return NewCell("", fx.Invoke(fn))
}

// OnStart registers a function to run on start up.
func OnStart(fn any) *Cell {
	typ := reflect.TypeOf(fn)

	in := []reflect.Type{reflect.TypeOf(new(fx.Lifecycle)).Elem()}
	for i := 0; i < typ.NumIn(); i++ {
		in = append(in, typ.In(i))
	}

	// Construct a wrapper function that takes the same arguments plus lifecycle.
	// The wrapper will append a start hook and will then invoke the original function
	// from the start hook.
	// We ignore any outputs from the function.
	funTyp := reflect.FuncOf(in, []reflect.Type{}, false)
	funVal := reflect.MakeFunc(funTyp, func(args []reflect.Value) []reflect.Value {
		lc := args[0].Interface().(fx.Lifecycle)
		lc.Append(fx.Hook{
			OnStart: func(context.Context) error {
				reflect.ValueOf(fn).Call(args[1:])
				return nil
			},
		})
		return []reflect.Value{}
	})
	return NewCell("", fx.Invoke(funVal.Interface()))
}

// Require constructs a cell that ensures the object T is
// always instantiated even if nothing refers to it.
func Require[T any]() *Cell {
	var v T
	typ := reflect.TypeOf(v)
	// Construct the function of type 'func(T)'
	funTyp := reflect.FuncOf([]reflect.Type{typ}, []reflect.Type{}, false)
	funVal := reflect.MakeFunc(funTyp, func([]reflect.Value) []reflect.Value {
		return []reflect.Value{}
	})
	return Invoke(funVal.Interface())
}

// NewCellWithConfig constructs a new cell with the name, configuration
// and options
//
// The configuration struct `T` needs to implement CellFlags method that
// registers the flags. The structure is populated and provided via dependency
// injection by Hive.Run(). The underlying mechanism for populating the struct
// is viper's Unmarshal().
func NewCellWithConfig[T CellConfig](name string, opts ...fx.Option) *Cell {
	var emptyConfig T
	return &Cell{
		name:          name,
		opts:          opts,
		registerFlags: emptyConfig.CellFlags,
		newConfig:     func() any { return emptyConfig },
	}
}
