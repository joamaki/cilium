// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package lbmap

import (
	"fmt"

	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/bpf"
	"github.com/cilium/cilium/pkg/datapath/linux/probes"
	datapathTypes "github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/option"
)

type Config struct {
	InitParams

	Maglev          bool
	MaglevTableSize int

	EnableSessionAffinity     bool
	EnableSVCSourceRangeCheck bool

	CreateSockRevNatMaps bool
	RestoreMaps          bool
}

var Module = fx.Module(
	"lbmap",
	fx.Provide(
		newLBMapConfig,     // *option.DaemonConfig -> Config
		newLBMapFromConfig, // Config -> LBMap
	),
)

func newLBMapConfig(daemonConfig *option.DaemonConfig) Config {
	lbmapInitParams := InitParams{
		IPv4: daemonConfig.EnableIPv4,
		IPv6: daemonConfig.EnableIPv6,

		MaxSockRevNatMapEntries:  daemonConfig.SockRevNatEntries,
		ServiceMapMaxEntries:     daemonConfig.LBMapEntries,
		BackEndMapMaxEntries:     daemonConfig.LBMapEntries,
		RevNatMapMaxEntries:      daemonConfig.LBMapEntries,
		AffinityMapMaxEntries:    daemonConfig.LBMapEntries,
		SourceRangeMapMaxEntries: daemonConfig.LBMapEntries,
		MaglevMapMaxEntries:      daemonConfig.LBMapEntries,
	}
	if daemonConfig.LBServiceMapEntries > 0 {
		lbmapInitParams.ServiceMapMaxEntries = daemonConfig.LBServiceMapEntries
	}
	if daemonConfig.LBBackendMapEntries > 0 {
		lbmapInitParams.BackEndMapMaxEntries = daemonConfig.LBBackendMapEntries
	}
	if daemonConfig.LBRevNatEntries > 0 {
		lbmapInitParams.RevNatMapMaxEntries = daemonConfig.LBRevNatEntries
	}
	if daemonConfig.LBAffinityMapEntries > 0 {
		lbmapInitParams.AffinityMapMaxEntries = daemonConfig.LBAffinityMapEntries
	}
	if daemonConfig.LBSourceRangeMapEntries > 0 {
		lbmapInitParams.SourceRangeMapMaxEntries = daemonConfig.LBSourceRangeMapEntries
	}
	if daemonConfig.LBMaglevMapEntries > 0 {
		lbmapInitParams.MaglevMapMaxEntries = daemonConfig.LBMaglevMapEntries
	}

	pm := probes.NewProbeManager()
	supportedMapTypes := pm.GetMapTypes()
	createSockRevNatMaps := daemonConfig.EnableSocketLB &&
		daemonConfig.EnableHostServicesUDP && supportedMapTypes.HaveLruHashMapType

	return Config{
		InitParams:                lbmapInitParams,
		Maglev:                    daemonConfig.NodePortAlg == option.NodePortAlgMaglev,
		MaglevTableSize:           daemonConfig.MaglevTableSize,
		EnableSessionAffinity:     daemonConfig.EnableSessionAffinity,
		EnableSVCSourceRangeCheck: daemonConfig.EnableSVCSourceRangeCheck,
		CreateSockRevNatMaps:      createSockRevNatMaps,
		RestoreMaps:               daemonConfig.RestoreState,
	}
}

func newLBMapFromConfig(lc fx.Lifecycle, config Config) (datapathTypes.LBMap, error) {
	// FIXME use config
	lbmap := New()

	// TODO: Initialization (e.g. map creation), should only
	// happen at start, but since the legacy daemon initialization
	// depends on lbmap being initialized before it, we initialize
	// the BPF maps here already.
	Init(config.InitParams)

	if config.Maglev {
		err := initMaglevMaps(config.IPv4, config.IPv6, uint32(config.MaglevTableSize))
		if err != nil {
			return nil, fmt.Errorf("initializing maglev maps: %w", err)
		}
	}

	if config.EnableSessionAffinity {
		if _, err := AffinityMatchMap.OpenOrCreate(); err != nil {
			return nil, err
		}
		if config.IPv4 {
			if _, err := Affinity4Map.OpenOrCreate(); err != nil {
				return nil, err
			}
		}
		if config.IPv6 {
			if _, err := Affinity6Map.OpenOrCreate(); err != nil {
				return nil, err
			}
		}
	}

	if config.EnableSVCSourceRangeCheck {
		if config.IPv4 {
			if _, err := SourceRange4Map.OpenOrCreate(); err != nil {
				return nil, err
			}
		}
		if config.IPv6 {
			if _, err := SourceRange6Map.OpenOrCreate(); err != nil {
				return nil, err
			}
		}
	}

	initServiceMaps(config)

	return lbmap, nil
}

func populateBackendMapV2FromV1(ipv4, ipv6 bool) error {
	const (
		v4 = "ipv4"
		v6 = "ipv6"
	)

	var (
		err   error
		v1Map *bpf.Map
	)

	enabled := map[string]bool{v4: ipv4, v6: ipv6}

	for v, e := range enabled {
		if !e {
			continue
		}

		copyBackendEntries := func(key bpf.MapKey, value bpf.MapValue) {
			var (
				v2Map        *bpf.Map
				v2BackendKey BackendKey
			)

			if v == v4 {
				backendKey := key.(BackendKey)
				v2Map = Backend4MapV2
				v2BackendKey = NewBackend4KeyV2(backendKey.GetID())
			} else {
				backendKey := key.(BackendKey)
				v2Map = Backend6MapV2
				v2BackendKey = NewBackend6KeyV2(backendKey.GetID())
			}

			err := v2Map.Update(v2BackendKey, value.DeepCopyMapValue())
			if err != nil {
				log.WithError(err).WithField(logfields.BPFMapName, v2Map.Name()).Warn("Error updating map")
			}
		}

		if v == v4 {
			v1Map = Backend4Map
		} else {
			v1Map = Backend6Map
		}

		err = v1Map.DumpWithCallback(copyBackendEntries)
		if err != nil {
			return fmt.Errorf("Unable to populate %s: %w", v1Map.Name(), err)
		}

		// V1 backend map will be removed from bpffs at this point,
		// the map will be actually removed once the last program
		// referencing it has been removed.
		err = v1Map.Close()
		if err != nil {
			log.WithError(err).WithField(logfields.BPFMapName, v1Map.Name()).Warn("Error closing map")
		}

		err = v1Map.Unpin()
		if err != nil {
			log.WithError(err).WithField(logfields.BPFMapName, v1Map.Name()).Warn("Error unpinning map")
		}

	}
	return nil
}

// InitMaps opens or creates BPF maps used by services.
//
// If restore is set to false, entries of the maps are removed.
func initServiceMaps(config Config) error {
	var (
		v1BackendMapExistsV4 bool
		v1BackendMapExistsV6 bool
	)

	toOpen := []*bpf.Map{}
	toDelete := []*bpf.Map{}
	if config.IPv6 {
		toOpen = append(toOpen, Service6MapV2, Backend6MapV2, RevNat6Map)
		if !config.RestoreMaps {
			toDelete = append(toDelete, Service6MapV2, Backend6MapV2, RevNat6Map)
		}
		if config.CreateSockRevNatMaps {
			if err := CreateSockRevNat6Map(); err != nil {
				return err
			}
		}
		v1BackendMapExistsV6 = Backend6Map.Open() == nil
	}
	if config.IPv4 {
		toOpen = append(toOpen, Service4MapV2, Backend4MapV2, RevNat4Map)
		if !config.RestoreMaps {
			toDelete = append(toDelete, Service4MapV2, Backend4MapV2, RevNat4Map)
		}
		if config.CreateSockRevNatMaps {
			if err := CreateSockRevNat4Map(); err != nil {
				return err
			}
		}
		v1BackendMapExistsV4 = Backend4Map.Open() == nil
	}

	for _, m := range toOpen {
		if _, err := m.OpenOrCreate(); err != nil {
			return err
		}
	}
	for _, m := range toDelete {
		if err := m.DeleteAll(); err != nil {
			return err
		}
	}

	if v1BackendMapExistsV4 || v1BackendMapExistsV6 {
		log.Info("Backend map v1 exists. Migrating entries to backend map v2.")
		if err := populateBackendMapV2FromV1(v1BackendMapExistsV4, v1BackendMapExistsV6); err != nil {
			log.WithError(err).Warn("Error populating V2 map from V1 map, might interrupt existing connections during upgrade")
		}
	}

	return nil
}
