// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

import (
	"context"
	"fmt"
	"reflect"

	"go.uber.org/fx"
)

// OnStart registers a start function of form "func(a A, b B, ...) error" to run on start up.
// The function can take any number of arguments.
//
// For example:
//
//	func myStartFunc(a A, b B) error
//	OnStart(myStartFunc)
//
// This will append a start hook to run myStartFunc after A and B have been constructed
// and their start hooks have been executed. This is equivalent to the long form:
//
//	Invoke(func(lc fx.Lifecycle, a A, b B) {
//		lc.Append(fx.Hook{OnStart: func(context.Context) error {
//			return myStartFunc(a, b)
//	  	})
//	}))
func OnStart(fn any) Cell {
	return onStartOrStop(true, fn)
}

// OnStop registers a stop function of form "func(a A, b B, ...) error" to run when stopping.
// The function can take any number of arguments.
func OnStop(fn any) Cell {
	return onStartOrStop(false, fn)
}

func onStartOrStop(start bool, fn any) Cell {
	typ := reflect.TypeOf(fn)
	if typ.Kind() != reflect.Func {
		panic(fmt.Sprintf("Called with unsupported type %s. Argument must be a function", typ))
	}

	in := []reflect.Type{reflect.TypeOf(new(fx.Lifecycle)).Elem()}
	for i := 0; i < typ.NumIn(); i++ {
		in = append(in, typ.In(i))
	}
	errType := reflect.TypeOf(new(error)).Elem()
	if typ.NumOut() != 1 || !typ.Out(0).Implements(errType) {
		panic(fmt.Sprintf("Called with unsupported type %s. Start function needs to return an error", typ))
	}

	// Construct an invoke function that takes the the 'fn's arguments plus a lifecycle.
	// The invoke function will then append a wrapper to the lifecycle that will call 'fn'
	// with the right arguments.
	funTyp := reflect.FuncOf(in, []reflect.Type{}, false)
	funVal := reflect.MakeFunc(funTyp, func(args []reflect.Value) []reflect.Value {
		lc := args[0].Interface().(fx.Lifecycle)
		wrapper := func(context.Context) error {
			result := reflect.ValueOf(fn).Call(args[1:])
			if result[0].IsNil() {
				return nil
			} else {
				return result[0].Interface().(error)
			}
		}
		if start {
			lc.Append(fx.Hook{OnStart: wrapper})
		} else {
			lc.Append(fx.Hook{OnStop: wrapper})
		}
		return []reflect.Value{}
	})
	return Invoke(funVal.Interface())
}

// Require constructs a cell that ensures the object T is
// always instantiated even if nothing refers to it.
func Require[T any]() Cell {
	var v T
	typ := reflect.TypeOf(v)
	// Construct the function of type 'func(T)'
	funTyp := reflect.FuncOf([]reflect.Type{typ}, []reflect.Type{}, false)
	funVal := reflect.MakeFunc(funTyp, func([]reflect.Value) []reflect.Value {
		return []reflect.Value{}
	})
	return Invoke(funVal.Interface())
}
