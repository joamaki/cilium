package lbmap

import (
	"fmt"

	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/option"
)

var Cell = cell.Module(
	"datapath-lbmap",
	"Load-balancer maps",
	cell.Provide(newForCell),
)

// TODO: remove this and modify New()
func newForCell(lc hive.Lifecycle, p InitParams) types.LBMap {
	lbmap := New()

	lc.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			return initLBMap(lbmap, p)
		},
		OnStop: func(hive.HookContext) error {
			return closeMaps()
		},
	})
	return lbmap
}

func initLBMap(m types.LBMap, p InitParams) error {
	Init(p)
	initRestOfTheMaps()
	return nil
}

// FIXME: move this copy-pasta from daemon/cmd/datapath.go to its right place
func initRestOfTheMaps() error {
	// FIXME: add config for these options. unclear where it should live
	// as control-plane requires it as well. datapath does seem cleaner
	// as it's "providing" these features.
	enableSessionAffinity := true
	enableIPv4 := true
	enableIPv6 := true
	enableSVCSourceRangeCheck := false
	nodePortAlg := option.NodePortAlgRandom
	maglevTableSize := 251

	if enableSessionAffinity {
		if _, err := AffinityMatchMap.OpenOrCreate(); err != nil {
			return err
		}
		if enableIPv4 {
			if _, err := Affinity4Map.OpenOrCreate(); err != nil {
				return err
			}
		}
		if enableIPv6 {
			if _, err := Affinity6Map.OpenOrCreate(); err != nil {
				return err
			}
		}
	}

	if enableSVCSourceRangeCheck {
		if enableIPv4 {
			if _, err := SourceRange4Map.OpenOrCreate(); err != nil {
				return err
			}
		}
		if enableIPv6 {
			if _, err := SourceRange6Map.OpenOrCreate(); err != nil {
				return err
			}
		}
	}

	if nodePortAlg == option.NodePortAlgMaglev {
		if err := InitMaglevMaps(enableIPv4, enableIPv6, uint32(maglevTableSize)); err != nil {
			return fmt.Errorf("initializing maglev maps: %w", err)
		}
	}
	return nil
}

// TODO: Remove the global map variables and implement proper Stop()
func closeMaps() error {
	if err := Service4MapV2.Close(); err != nil {
		return err
	}
	if err := Backend4Map.Close(); err != nil {
		return err
	}
	if err := Backend4MapV3.Close(); err != nil {
		return err
	}
	// FIXME the rest
	return nil
}
