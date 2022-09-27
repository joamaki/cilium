// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

import (
	"github.com/spf13/pflag"
	"go.uber.org/fx"
)

// Cell is a modular component of the hive.
// A cell can be:
//   - Cells: Set of cells.
//   - Provider: Cell providing object constructors (with fx.Provide etc.)
//   - Decorator: Decorated set of cells. E.g. for providing custom lifecycle or other object to a
//     specific set of cells.
//   - Config: Cell providing a configuration struct.
type Cell interface {
	RegisterFlags(*pflag.FlagSet)
	ToOption(settings map[string]any) (fx.Option, error)
}

type In = fx.In
type Out = fx.Out

// providerCell is a named cell for a set of constructors
type providerCell struct {
	name  string
	ctors []any
}

func (c *providerCell) RegisterFlags(flags *pflag.FlagSet) {
}

func (c *providerCell) ToOption(settings map[string]any) (fx.Option, error) {
	return fx.Module(c.name, fx.Provide(c.ctors...)), nil
}

// Provide constructs a new named cell with the given name and constructors.
// Constructor is any function that takes zero or more parameters and returns
// one or more values and optionally an error. For example, the following forms
// are accepted:
//
//	func() A
//	func(A, B, C) (D, error).
//
// If the constructor depends on a type that is not provided by any constructor
// the hive will fail to run with an error pointing at the missing type.
//
// A constructor can also take as parameter a structure of parameters annotated
// with `cell.In`, or return a struct annotated with `cell.Out`:
//
//	type params struct {
//		cell.In
//		Flower *Flower
//		Sun *Sun
//	}
//
//	type out struct {
//		cell.Out
//		Honey *Honey
//		Nectar *Nectar
//	}
//
//	func newBee(params) (out, error)
func Provide(ctors ...any) Cell {
	return &providerCell{ctors: ctors}
}

// groupCell is a named set of cells. Used for grouping cells into one value.
type groupCell struct {
	name  string
	cells []Cell
}

// Group creates a named group of cells. Groups are useful for passing
// a set of constructors and invokes around. The name will be included
// in the object dump (hive.PrintObjects) and in the dot graph (hive.PrintDotGraph).
func Group(name string, cells ...Cell) Cell {
	return &groupCell{name, cells}
}

func (g *groupCell) RegisterFlags(flags *pflag.FlagSet) {
	for _, cell := range g.cells {
		cell.RegisterFlags(flags)
	}
}

func (g *groupCell) ToOption(settings map[string]any) (fx.Option, error) {
	opts := []fx.Option{}
	for _, cell := range g.cells {
		opt, err := cell.ToOption(settings)
		if err != nil {
			return nil, err
		}
		opts = append(opts, opt)
	}
	return fx.Module(g.name, opts...), nil
}
