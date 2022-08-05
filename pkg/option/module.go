// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package option

import (
	"fmt"
	"os"
	"reflect"

	"github.com/spf13/pflag"
	"go.uber.org/fx"
)

// The option module implements a modular configuration system
var Module = fx.Module(
	"option",

	fx.Provide(newPflagConfigProvider),
)

type ConfigProviderParams struct {
	fx.In

	Frags []ConfigFragment `group:"fragments"`
}

type configProvider struct {
	configs map[string]reflect.Value
}

func (p *configProvider) GetConfigFor(proto any) (reflect.Value, error) {
	typeName := reflect.TypeOf(proto).Name()

	if cfg, ok := p.configs[typeName]; ok {
		return cfg, nil
	}
	return reflect.ValueOf(nil), fmt.Errorf("No configuration found for %s", typeName)
}

func newPflagConfigProvider(in ConfigProviderParams) (ConfigProvider, error) {
	fs := pflag.NewFlagSet("option", pflag.ContinueOnError)

	configs := make(map[string]reflect.Value)

	// Iterate over all registered configuration fragments to build the flag set.
	for _, frag := range in.Frags {
		val := reflect.ValueOf(frag.defaultValue)
		typ := val.Type()

		parsedValue := reflect.New(typ)
		configs[typ.Name()] = parsedValue
		parsedValue = parsedValue.Elem() // deref

		if val.Kind() != reflect.Struct {
			return nil, fmt.Errorf("%s is not a struct!", typ)
		}

		for i := 0; i < val.NumField(); i++ {
			fieldValue := val.Field(i)
			field := typ.Field(i)
			fieldDefault := fieldValue.Interface()

			flagTag := field.Tag.Get("flag")
			if flagTag == "" {
				return nil, fmt.Errorf("%s.%s is missing flag tag", typ.Name(), field.Name)
			}

			// TODO handle opts

			usageTag := field.Tag.Get("usage")
			if usageTag == "" {
				return nil, fmt.Errorf("%s.%s is missing usage tag", typ.Name(), field.Name)
			}

			fieldPtr := parsedValue.Field(i).Addr().Interface()
			switch field.Type.Kind() {
			case reflect.String:
				fs.StringVar(fieldPtr.(*string), flagTag, fieldDefault.(string), usageTag)
			case reflect.Int:
				fs.IntVar(fieldPtr.(*int), flagTag, fieldDefault.(int), usageTag)
			case reflect.Float32:
				fs.Float32Var(fieldPtr.(*float32), flagTag, fieldDefault.(float32), usageTag)
			case reflect.Bool:
				fs.BoolVar(fieldPtr.(*bool), flagTag, fieldDefault.(bool), usageTag)
			default:
				return nil, fmt.Errorf("%s.%s has unsupported config variable type %s", typ.Name(), field.Name, field.Type.Name())
			}
		}
	}

	// Parse the command-line flags.
	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, err
	}
	return &configProvider{configs}, nil
}

type ConfigFragment struct {
	defaultValue any
}

type ConfigFragmentOut struct {
	fx.Out
	ConfigFragment `group:"fragments"`
}

func newConfigFragment[T any](def T) ConfigFragmentOut {
	// TODO validate that this thing is a struct and that
	// all fields have flag and usage
	return ConfigFragmentOut{ConfigFragment: ConfigFragment{def}}
}

type ConfigProvider interface {
	GetConfigFor(proto any) (reflect.Value, error)
}

func getConfig[T any](p ConfigProvider) (T, error) {
	var proto T
	if cfgValue, err := p.GetConfigFor(proto); err != nil {
		return proto, err
	} else {
		return cfgValue.Elem().Interface().(T), nil
	}
}

func ConfigOption[T any](defaultValue T) fx.Option {
	return fx.Options(
		// Supply the config structure and the defaults to the config provider.
		fx.Supply(newConfigFragment(defaultValue)),
		// Register the constructor for the configuration that pulls it from
		// config provider after it has been parsed.
		fx.Provide(getConfig[T]))
}

/*
type ExampleConfig struct {
	FooOpt string `flag:"foo" usage:"Documentation for foo"`
	BarOpt string `flag:"bar" opts:"hidden" usage:"Documentation for bar"`
}
var exampleConfigDefault = ExampleConfig{"default foo", "default bar"}

var ExampleModule = fx.Module(
	"example",

	// Register the configuration struct with defaults
	ConfigOption(exampleConfigDefault),

	fx.Invoke(func(config ExampleConfig) {
		  // do something with config
	}),
)
*/

