// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"

	"github.com/cilium/hive/cell"

	"github.com/cilium/cilium/pkg/ebpf"
	"github.com/cilium/cilium/pkg/maps/lbmap"
	"github.com/cilium/cilium/pkg/option"
)

// LBMapsConfig specifies the configuration for the load-balancing BPF
// maps.
type LBMapsConfig struct {
	MaxSockRevNatMapEntries                                         int
	ServiceMapMaxEntries, BackendMapMaxEntries, RevNatMapMaxEntries int
	AffinityMapMaxEntries                                           int
	SourceRangeMapMaxEntries                                        int
	MaglevMapMaxEntries                                             int
}

// newLBMapsConfig creates the config from the DaemonConfig. When we
// move to the new implementation this should be replaced with a cell.Config.
func newLBMapsConfig(dcfg *option.DaemonConfig) (cfg LBMapsConfig) {
	cfg.MaxSockRevNatMapEntries = dcfg.SockRevNatEntries
	cfg.ServiceMapMaxEntries = dcfg.LBMapEntries
	cfg.BackendMapMaxEntries = dcfg.LBMapEntries
	cfg.RevNatMapMaxEntries = dcfg.LBMapEntries
	cfg.AffinityMapMaxEntries = dcfg.LBMapEntries
	cfg.SourceRangeMapMaxEntries = dcfg.LBMapEntries
	cfg.MaglevMapMaxEntries = dcfg.LBMapEntries
	if dcfg.LBServiceMapEntries > 0 {
		cfg.ServiceMapMaxEntries = dcfg.LBServiceMapEntries
	}
	if dcfg.LBBackendMapEntries > 0 {
		cfg.BackendMapMaxEntries = dcfg.LBBackendMapEntries
	}
	if dcfg.LBRevNatEntries > 0 {
		cfg.RevNatMapMaxEntries = dcfg.LBRevNatEntries
	}
	if dcfg.LBAffinityMapEntries > 0 {
		cfg.AffinityMapMaxEntries = dcfg.LBAffinityMapEntries
	}
	if dcfg.LBSourceRangeMapEntries > 0 {
		cfg.SourceRangeMapMaxEntries = dcfg.LBSourceRangeMapEntries
	}
	if dcfg.LBMaglevMapEntries > 0 {
		cfg.MaglevMapMaxEntries = dcfg.LBMaglevMapEntries
	}
	return
}

type serviceMaps interface {
	UpdateService(key lbmap.ServiceKey, value lbmap.ServiceValue) error
	DeleteService(key lbmap.ServiceKey) error
	DumpService(cb func(lbmap.ServiceKey, lbmap.ServiceValue)) error
}

type backendMaps interface {
	UpdateBackend(lbmap.BackendKey, lbmap.BackendValue) error
	DeleteBackend(lbmap.BackendKey) error
	DumpBackend(cb func(lbmap.BackendKey, lbmap.BackendValue)) error
}

type revNatMaps interface {
	UpdateRevNat(lbmap.RevNatKey, lbmap.RevNatValue) error
	DeleteRevNat(lbmap.RevNatKey) error
	DumpRevNat(cb func(lbmap.RevNatKey, lbmap.RevNatValue)) error
}

type affinityMaps interface {
	UpdateAffinityMatch(*lbmap.AffinityMatchKey, *lbmap.AffinityMatchValue) error
	DeleteAffinityMatch(*lbmap.AffinityMatchKey) error
	DumpAffinityMatch(cb func(*lbmap.AffinityMatchKey, *lbmap.AffinityMatchValue)) error
}

type sourceRangeMaps interface {
	UpdateSourceRange(lbmap.SourceRangeKey, *lbmap.SourceRangeValue) error
	DeleteSourceRange(lbmap.SourceRangeKey) error
	DumpSourceRange(cb func(lbmap.SourceRangeKey, *lbmap.SourceRangeValue)) error
}

// lbmaps defines the map operations performed by the reconciliation.
// Depending on this interface instead of on the underlying maps allows
// testing the implementation with a fake map or injected errors.
type lbmaps interface {
	serviceMaps
	backendMaps
	revNatMaps
	affinityMaps
	sourceRangeMaps

	// TODO rest of the maps:
	// Maglev, SockRevNat, SkipLB
}

type realLBMaps struct {
	// pinned if true will pin the maps to a file. Tests may turn this off.
	pinned bool

	cfg LBMapsConfig

	service4Map, service6Map         *ebpf.Map
	backend4Map, backend6Map         *ebpf.Map
	revNat4Map, revNat6Map           *ebpf.Map
	affinityMatchMap                 *ebpf.Map
	sourceRange4Map, sourceRange6Map *ebpf.Map
}

func sizeOf[T any]() uint32 {
	var x T
	return uint32(reflect.TypeOf(x).Size())
}

// BPF map specs
var (
	service4MapSpec = &ebpf.MapSpec{
		Name:      lbmap.Service4MapV2Name,
		Type:      ebpf.Hash,
		KeySize:   sizeOf[lbmap.Service4Key](),
		ValueSize: sizeOf[lbmap.Service4Value](),
	}

	service6MapSpec = &ebpf.MapSpec{
		Name:      lbmap.Service6MapV2Name,
		Type:      ebpf.Hash,
		KeySize:   sizeOf[lbmap.Service6Key](),
		ValueSize: sizeOf[lbmap.Service6Value](),
	}

	backend4MapSpec = &ebpf.MapSpec{
		Name:      lbmap.Backend4MapV3Name,
		Type:      ebpf.Hash,
		KeySize:   sizeOf[lbmap.Backend4KeyV3](),
		ValueSize: sizeOf[lbmap.Backend4ValueV3](),
	}

	backend6MapSpec = &ebpf.MapSpec{
		Name:      lbmap.Backend6MapV3Name,
		Type:      ebpf.Hash,
		KeySize:   sizeOf[lbmap.Backend6KeyV3](),
		ValueSize: sizeOf[lbmap.Backend6ValueV3](),
	}

	revNat4MapSpec = &ebpf.MapSpec{
		Name:      lbmap.RevNat4MapName,
		Type:      ebpf.Hash,
		KeySize:   sizeOf[lbmap.RevNat4Key](),
		ValueSize: sizeOf[lbmap.RevNat4Value](),
	}

	revNat6MapSpec = &ebpf.MapSpec{
		Name:      lbmap.RevNat6MapName,
		Type:      ebpf.Hash,
		KeySize:   sizeOf[lbmap.RevNat6Key](),
		ValueSize: sizeOf[lbmap.RevNat6Value](),
	}

	affinityMatchMapSpec = &ebpf.MapSpec{
		Name:      lbmap.AffinityMatchMapName,
		Type:      ebpf.Hash,
		KeySize:   sizeOf[lbmap.AffinityMatchKey](),
		ValueSize: sizeOf[lbmap.AffinityMatchValue](),
	}

	sourceRange4MapSpec = &ebpf.MapSpec{
		Name:      lbmap.SourceRange4MapName,
		Type:      ebpf.LPMTrie,
		KeySize:   sizeOf[lbmap.SourceRangeKey4](),
		ValueSize: sizeOf[lbmap.SourceRangeValue](),
	}

	sourceRange6MapSpec = &ebpf.MapSpec{
		Name:      lbmap.SourceRange6MapName,
		Type:      ebpf.LPMTrie,
		KeySize:   sizeOf[lbmap.SourceRangeKey6](),
		ValueSize: sizeOf[lbmap.SourceRangeValue](),
	}
)

type mapDesc struct {
	target     **ebpf.Map // pointer to the field in realLBMaps
	spec       *ebpf.MapSpec
	maxEntries int
}

func (r *realLBMaps) allMaps() []mapDesc {
	return []mapDesc{
		{&r.service4Map, service4MapSpec, r.cfg.ServiceMapMaxEntries},
		{&r.service6Map, service6MapSpec, r.cfg.ServiceMapMaxEntries},
		{&r.backend4Map, backend4MapSpec, r.cfg.BackendMapMaxEntries},
		{&r.backend6Map, backend6MapSpec, r.cfg.BackendMapMaxEntries},
		{&r.revNat4Map, revNat4MapSpec, r.cfg.RevNatMapMaxEntries},
		{&r.revNat6Map, revNat6MapSpec, r.cfg.RevNatMapMaxEntries},
		{&r.affinityMatchMap, affinityMatchMapSpec, r.cfg.AffinityMapMaxEntries},
		{&r.sourceRange4Map, sourceRange4MapSpec, r.cfg.SourceRangeMapMaxEntries},
		{&r.sourceRange6Map, sourceRange6MapSpec, r.cfg.SourceRangeMapMaxEntries},
	}
}

// Start implements cell.HookInterface.
func (r *realLBMaps) Start(cell.HookContext) error {
	for _, desc := range r.allMaps() {
		if r.pinned {
			desc.spec.Pinning = ebpf.PinByName
		} else {
			desc.spec.Pinning = ebpf.PinNone
		}
		desc.spec.MaxEntries = uint32(desc.maxEntries)
		m := ebpf.NewMap(desc.spec)
		*desc.target = m

		if err := m.OpenOrCreate(); err != nil {
			return fmt.Errorf("opening map %s: %w", desc.spec.Name, err)
		}
	}
	return nil
}

// Stop implements cell.HookInterface.
func (r *realLBMaps) Stop(cell.HookContext) error {
	var errs []error
	for _, desc := range r.allMaps() {
		m := *desc.target
		if m != nil {
			if err := m.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// DeleteRevNat implements lbmaps.
func (r *realLBMaps) DeleteRevNat(key lbmap.RevNatKey) error {
	var err error
	switch key.(type) {
	case *lbmap.RevNat4Key:
		err = r.revNat4Map.Delete(key)
	case *lbmap.RevNat6Key:
		err = r.revNat6Map.Delete(key)
	default:
		panic("unknown RevNatKey")
	}
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}
	return err
}

// DumpRevNat implements lbmaps.
func (r *realLBMaps) DumpRevNat(cb func(lbmap.RevNatKey, lbmap.RevNatValue)) error {
	cbWrap := func(key, value any) {
		cb(
			key.(lbmap.RevNatKey).ToHost(),
			value.(lbmap.RevNatValue).ToHost(),
		)
	}
	return errors.Join(
		r.revNat4Map.IterateWithCallback(&lbmap.RevNat4Key{}, &lbmap.RevNat4Value{}, cbWrap),
		r.revNat6Map.IterateWithCallback(&lbmap.RevNat6Key{}, &lbmap.RevNat6Value{}, cbWrap),
	)
}

// UpdateRevNat4 implements lbmaps.
func (r *realLBMaps) UpdateRevNat(key lbmap.RevNatKey, value lbmap.RevNatValue) error {
	switch key.(type) {
	case *lbmap.RevNat4Key:
		return r.revNat4Map.Update(key, value, 0)
	case *lbmap.RevNat6Key:
		return r.revNat6Map.Update(key, value, 0)
	default:
		panic("unknown RevNatKey")
	}
}

// DumpBackend implements lbmaps.
func (r *realLBMaps) DumpBackend(cb func(lbmap.BackendKey, lbmap.BackendValue)) error {
	cbWrap := func(key, value any) {
		cb(
			key.(lbmap.BackendKey),
			value.(lbmap.BackendValue).ToHost(),
		)
	}
	return errors.Join(
		r.backend4Map.IterateWithCallback(&lbmap.Backend4KeyV3{}, &lbmap.Backend4ValueV3{}, cbWrap),
		r.backend6Map.IterateWithCallback(&lbmap.Backend6KeyV3{}, &lbmap.Backend6ValueV3{}, cbWrap),
	)
}

// DeleteBackend implements lbmaps.
func (r *realLBMaps) DeleteBackend(key lbmap.BackendKey) error {
	var err error
	switch key.(type) {
	case *lbmap.Backend4KeyV3:
		err = r.backend4Map.Delete(key)
	case *lbmap.Backend6KeyV3:
		err = r.backend6Map.Delete(key)
	default:
		panic("unknown BackendKey")
	}
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}
	return err
}

// DeleteService implements lbmaps.
func (r *realLBMaps) DeleteService(key lbmap.ServiceKey) error {
	var err error
	switch key.(type) {
	case *lbmap.Service4Key:
		err = r.service4Map.Delete(key)
	case *lbmap.Service6Key:
		err = r.service6Map.Delete(key)
	default:
		panic("unknown ServiceKey")
	}
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}
	return err
}

// DumpService implements lbmaps.
func (r *realLBMaps) DumpService(cb func(lbmap.ServiceKey, lbmap.ServiceValue)) error {
	cbWrap := func(key, value any) {
		svcKey := key.(lbmap.ServiceKey).ToHost()
		svcValue := value.(lbmap.ServiceValue).ToHost()
		cb(svcKey, svcValue)
	}

	return errors.Join(
		r.service4Map.IterateWithCallback(&lbmap.Service4Key{}, &lbmap.Service4Value{}, cbWrap),
		r.service6Map.IterateWithCallback(&lbmap.Service6Key{}, &lbmap.Service6Value{}, cbWrap),
	)
}

// UpdateBackend implements lbmaps.
func (r *realLBMaps) UpdateBackend(key lbmap.BackendKey, value lbmap.BackendValue) error {
	switch key.(type) {
	case *lbmap.Backend4KeyV3:
		return r.backend4Map.Update(key, value, 0)
	case *lbmap.Backend6KeyV3:
		return r.backend6Map.Update(key, value, 0)
	default:
		panic("unknown BackendKey")
	}
}

// UpdateService implements lbmaps.
func (r *realLBMaps) UpdateService(key lbmap.ServiceKey, value lbmap.ServiceValue) error {
	switch key.(type) {
	case *lbmap.Service4Key:
		return r.service4Map.Update(key, value, 0)
	case *lbmap.Service6Key:
		return r.service6Map.Update(key, value, 0)
	default:
		panic("unknown ServiceKey")
	}
}

// DeleteAffinityMatch implements lbmaps.
func (r *realLBMaps) DeleteAffinityMatch(key *lbmap.AffinityMatchKey) error {
	err := r.affinityMatchMap.Delete(key)
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}
	return err
}

// DumpAffinityMatch implements lbmaps.
func (r *realLBMaps) DumpAffinityMatch(cb func(*lbmap.AffinityMatchKey, *lbmap.AffinityMatchValue)) error {
	cbWrap := func(key, value any) {
		affKey := key.(*lbmap.AffinityMatchKey).ToHost()
		affValue := value.(*lbmap.AffinityMatchValue)
		cb(affKey, affValue)
	}
	return r.affinityMatchMap.IterateWithCallback(
		&lbmap.AffinityMatchKey{},
		&lbmap.AffinityMatchValue{},
		cbWrap,
	)
}

// UpdateAffinityMatch implements lbmaps.
func (r *realLBMaps) UpdateAffinityMatch(key *lbmap.AffinityMatchKey, value *lbmap.AffinityMatchValue) error {
	return r.affinityMatchMap.Update(key, value, 0)
}

// DeleteSourceRange implements lbmaps.
func (r *realLBMaps) DeleteSourceRange(key lbmap.SourceRangeKey) error {
	var err error
	switch key.(type) {
	case *lbmap.SourceRangeKey4:
		err = r.sourceRange4Map.Delete(key)
	case *lbmap.SourceRangeKey6:
		err = r.sourceRange6Map.Delete(key)
	default:
		panic("unknown SourceRangeKey")
	}
	if errors.Is(err, ebpf.ErrKeyNotExist) {
		return nil
	}
	return err
}

// DumpSourceRange implements lbmaps.
func (r *realLBMaps) DumpSourceRange(cb func(lbmap.SourceRangeKey, *lbmap.SourceRangeValue)) error {
	cbWrap := func(key, value any) {
		svcKey := key.(lbmap.SourceRangeKey).ToHost()
		svcValue := value.(*lbmap.SourceRangeValue)

		cb(svcKey, svcValue)
	}

	return errors.Join(
		r.sourceRange4Map.IterateWithCallback(&lbmap.SourceRangeKey4{}, &lbmap.SourceRangeValue{}, cbWrap),
		r.sourceRange6Map.IterateWithCallback(&lbmap.SourceRangeKey6{}, &lbmap.SourceRangeValue{}, cbWrap),
	)
}

// UpdateSourceRange implements lbmaps.
func (r *realLBMaps) UpdateSourceRange(key lbmap.SourceRangeKey, value *lbmap.SourceRangeValue) error {
	switch key.(type) {
	case *lbmap.SourceRangeKey4:
		return r.sourceRange4Map.Update(key, value, 0)
	case *lbmap.SourceRangeKey6:
		return r.sourceRange6Map.Update(key, value, 0)
	default:
		panic("unknown SourceRangeKey")
	}
}

var _ lbmaps = &realLBMaps{}

type faultyLBMaps struct {
	impl lbmaps

	// 0.0 (never fail) ... 1.0 (always fail)
	failureProbability float32
}

// DeleteSourceRange implements lbmaps.
func (f *faultyLBMaps) DeleteSourceRange(key lbmap.SourceRangeKey) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.DeleteSourceRange(key)
}

// DumpSourceRange implements lbmaps.
func (f *faultyLBMaps) DumpSourceRange(cb func(lbmap.SourceRangeKey, *lbmap.SourceRangeValue)) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.DumpSourceRange(cb)
}

// UpdateSourceRange implements lbmaps.
func (f *faultyLBMaps) UpdateSourceRange(key lbmap.SourceRangeKey, value *lbmap.SourceRangeValue) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.UpdateSourceRange(key, value)
}

// DeleteAffinityMatch implements lbmaps.
func (f *faultyLBMaps) DeleteAffinityMatch(key *lbmap.AffinityMatchKey) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.DeleteAffinityMatch(key)
}

// DumpAffinityMatch implements lbmaps.
func (f *faultyLBMaps) DumpAffinityMatch(cb func(*lbmap.AffinityMatchKey, *lbmap.AffinityMatchValue)) error {
	return f.impl.DumpAffinityMatch(cb)
}

// UpdateAffinityMatch implements lbmaps.
func (f *faultyLBMaps) UpdateAffinityMatch(key *lbmap.AffinityMatchKey, value *lbmap.AffinityMatchValue) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.UpdateAffinityMatch(key, value)
}

// DeleteRevNat implements lbmaps.
func (f *faultyLBMaps) DeleteRevNat(key lbmap.RevNatKey) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.DeleteRevNat(key)
}

// DumpRevNat implements lbmaps.
func (f *faultyLBMaps) DumpRevNat(cb func(lbmap.RevNatKey, lbmap.RevNatValue)) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.DumpRevNat(cb)
}

// UpdateRevNat implements lbmaps.
func (f *faultyLBMaps) UpdateRevNat(key lbmap.RevNatKey, value lbmap.RevNatValue) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.UpdateRevNat(key, value)
}

// DeleteBackend implements lbmaps.
func (f *faultyLBMaps) DeleteBackend(key lbmap.BackendKey) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.DeleteBackend(key)
}

// DeleteService implements lbmaps.
func (f *faultyLBMaps) DeleteService(key lbmap.ServiceKey) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.DeleteService(key)
}

// DumpBackend implements lbmaps.
func (f *faultyLBMaps) DumpBackend(cb func(lbmap.BackendKey, lbmap.BackendValue)) error {
	return f.impl.DumpBackend(cb)
}

// DumpService implements lbmaps.
func (f *faultyLBMaps) DumpService(cb func(lbmap.ServiceKey, lbmap.ServiceValue)) error {
	return f.impl.DumpService(cb)
}

// UpdateBackend implements lbmaps.
func (f *faultyLBMaps) UpdateBackend(key lbmap.BackendKey, value lbmap.BackendValue) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.UpdateBackend(key, value)
}

// UpdateService implements lbmaps.
func (f *faultyLBMaps) UpdateService(key lbmap.ServiceKey, value lbmap.ServiceValue) error {
	if f.isFaulty() {
		return errFaulty
	}
	return f.impl.UpdateService(key, value)
}

func (f *faultyLBMaps) isFaulty() bool {
	// Float32() returns value between [0.0, 1.0).
	// We fail if the value is less than our probability [0.0, 1.0].
	return f.failureProbability > rand.Float32()
}

var errFaulty = errors.New("faulty")

var _ lbmaps = &faultyLBMaps{}
