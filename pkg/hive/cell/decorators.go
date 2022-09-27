// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

import (
	"github.com/spf13/pflag"
	"go.uber.org/fx"
)

type decoratorCell struct {
	decorator fx.Option
	cells     []Cell
}

func (d *decoratorCell) RegisterFlags(flags *pflag.FlagSet) {
	for _, cell := range d.cells {
		cell.RegisterFlags(flags)
	}
}

func (d *decoratorCell) ToOption(settings map[string]any) (fx.Option, error) {
	opts := []fx.Option{d.decorator}
	for _, cell := range d.cells {
		opt, err := cell.ToOption(settings)
		if err != nil {
			return nil, err
		}
		opts = append(opts, opt)
	}
	return fx.Module("", opts...), nil
}

// Decorate takes a decorator function and a set of cells and returns
// a decorator cell.
//
// A decorator function is a function that takes as argument an object
// in the hive and returns an object of the same type. The cells wrapped
// with a decorator will be provided this decorated object.
func Decorate(decorator any, cells ...Cell) Cell {
	return &decoratorCell{
		decorator: fx.Decorate(decorator),
		cells:     cells,
	}
}

// WithCustomLifecycle wraps a set of cells with a custom lifecycle.
// Primary use-case for this helper is in operator which has two lifecycles:
// an outer lifecycle for infrastructure and inner lifecycle for when it
// is elected leader.
func WithCustomLifecycle(lc fx.Lifecycle, cells ...Cell) Cell {
	return Decorate(
		func(_ fx.Lifecycle) fx.Lifecycle {
			return lc
		},
		cells...)
}
