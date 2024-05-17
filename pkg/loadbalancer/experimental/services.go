// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/cilium/statedb"
	"github.com/cilium/statedb/reconciler"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/cilium/cilium/pkg/container"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/source"
)

// Services provides validated write access to the frontend and backend tables.
//
// This is currently experimental and not in use by default.
// See comment in [tables.go] for further context.
type Services struct {
	log   *slog.Logger
	db    *statedb.DB
	fes   statedb.RWTable[*Frontend]
	bes   statedb.RWTable[*Backend]
	hooks []FrontendUpsertHook
}

func NewServices(cfg Config, log *slog.Logger, db *statedb.DB, fes statedb.RWTable[*Frontend], bes statedb.RWTable[*Backend]) (*Services, error) {
	if !cfg.EnableNewServices {
		return nil, nil
	}
	return &Services{
		log:   log,
		db:    db,
		fes:   fes,
		bes:   bes,
		hooks: []FrontendUpsertHook{},
	}, nil
}

func (s *Services) IsEnabled() bool {
	return s != nil
}

// FrontendUpsertHook is a callback that runs when a frontend is upserted. It can perform reads using the provided
// ReadTxn, but for consistency it must not depend on any other data (except when the data is constant).
// The hook can modify the frontend object.
type FrontendUpsertHook func(statedb.ReadTxn, *Frontend)

type ServiceWriteTxn struct {
	statedb.WriteTxn
}

var (
	ErrFrontendSourceMismatch = errors.New("frontend exists with different source")
	ErrFrontendConflict       = errors.New("conflict with a service sharing address with a different name")
)

// RegisterHook adds a new hook to modify a service when upserted. This must only be called from
// providers/invokes and not after starting!
func (s *Services) RegisterHook(h FrontendUpsertHook) {
	s.hooks = append(s.hooks, h)
}

// Frontends returns the frontend table for reading.
// Convenience method for reducing dependencies.
func (s *Services) Frontends() statedb.Table[*Frontend] {
	return s.fes
}

// Services returns the backend table for reading.
// Convenience method for reducing dependencies.
func (s *Services) Backends() statedb.Table[*Backend] {
	return s.bes
}

// WriteTxn returns a write transaction against services & backends and other additional
// tables to be used with the methods of [Services]. The returned transaction MUST be
// Abort()'ed or Commit()'ed.
func (s *Services) WriteTxn(extraTables ...statedb.TableMeta) ServiceWriteTxn {
	return ServiceWriteTxn{
		s.db.WriteTxn(s.fes, append(extraTables, s.bes)...),
	}
}

func (s *Services) UpsertFrontend(txn ServiceWriteTxn, params *FrontendParams) (created bool, err error) {
	var fe Frontend

	existing, _, found := s.fes.Get(txn, FrontendL3n4AddrIndex.Query(params.Address))
	if found {
		if params == existing.FrontendParams {
			// FrontendParams was not cloned. It is likely that someone modified it which is a
			// data race. Hard panic so this is catched immediately during development.
			panic("BUG: UpsertFrontend called with a non-Clone()'d FrontendParams. Clone it before modifying it!")
		}

		// Do not allow frontends with the same address but different names to override each other.
		if !existing.Name.Equal(params.Name) {
			return false, ErrFrontendConflict
		}

		// Do not allow overriding a frontend that has been created from
		// another source.
		if existing.Source != params.Source {
			return false, ErrFrontendSourceMismatch
		}

		// Keep the existing fields.
		fe = *existing
	}
	created = !found

	fe.FrontendParams = params

	// Mark the frontend for reconciliation.
	fe.Status = reconciler.StatusPending()

	// Run hooks
	for _, hook := range s.hooks {
		hook(txn, &fe)
	}

	_, _, err = s.fes.Insert(txn, &fe)
	return
}

// UpsertFrontendAndAllBackends updates both a frontend and ALL its associated backends. If a backend exists that
// is associated with the frontend and it is not included in this call, then it will be released.
//
// TODO: This is needed to implement a "ServiceManager" compatible API that combines the frontend and backends.
// Unclear whether this is an API we would like to support at all as this doesn't work at all with merging e.g.
// remote backends. My current preference is that the data sources themselves need to manage
// their active sets of frontends and backends and figure out when a backend needs to be released. For k8s
// this would mean the k8s controller would need to figure out the delta from a EndpointSlice update (keep around
// some state for this).
func (s *Services) UpsertFrontendAndAllBackends(txn ServiceWriteTxn, params *FrontendParams, bes ...*loadbalancer.Backend) (created bool, err error) {
	created, err = s.UpsertFrontend(txn, params)
	if err != nil {
		return
	}

	beAddrs := sets.New[loadbalancer.L3n4Addr]()
	for _, be := range bes {
		beAddrs.Insert(be.L3n4Addr)
	}

	// [bes] is the complete set of backends for this frontend.
	// Release backends not included in the new set.
	iter := s.bes.List(txn, BackendServiceIndex.Query(params.Name))
	for be, _, ok := iter.Next(); ok; be, _, ok = iter.Next() {
		if !params.Address.ProtoEqual(&be.L3n4Addr) || beAddrs.Has(be.L3n4Addr) {
			continue
		}
		if err := s.removeBackendRef(txn, params.Name, be); err != nil {
			return created, err
		}
	}

	// Upsert the new set of backends.
	err = s.UpsertBackends(txn, params.Name, params.Source, bes...)
	return
}

func (s *Services) DeleteFrontend(txn ServiceWriteTxn, name loadbalancer.ServiceName, addr loadbalancer.L3n4Addr) (old *Frontend, err error) {
	old, _, found := s.fes.Get(txn, FrontendL3n4AddrIndex.Query(addr))
	if !found {
		return nil, nil
	}
	if !old.Name.Equal(name) {
		return nil, ErrFrontendConflict
	}
	err = s.deleteFrontend(txn, old)
	return
}

func (s *Services) DeleteFrontendsByName(txn ServiceWriteTxn, name loadbalancer.ServiceName, source source.Source) error {
	svc, _, found := s.fes.Get(txn, FrontendNameIndex.Query(name))
	if !found {
		return statedb.ErrObjectNotFound
	}
	if svc.Source != source {
		return ErrFrontendSourceMismatch
	}

	return s.deleteFrontend(txn, svc)
}

func (s *Services) deleteFrontend(txn ServiceWriteTxn, svc *Frontend) error {
	// Release references to the backends
	iter := s.bes.List(txn, BackendServiceIndex.Query(svc.Name))
	for be, _, ok := iter.Next(); ok; be, _, ok = iter.Next() {
		be, orphan := be.removeRef(svc.Name)
		if orphan {
			if _, _, err := s.bes.Delete(txn, be); err != nil {
				return err
			}
		} else {
			if _, _, err := s.bes.Insert(txn, be); err != nil {
				return err
			}
		}
	}
	_, _, err := s.fes.Delete(txn, svc)
	return err
}

// DeleteFrontendsBySource deletes all frontends from the specific source. This is used to
// implement "resynchronization", for example with K8s when the Watch() call fails and we need
// to start over with a List().
func (s *Services) DeleteFrontendsBySource(txn ServiceWriteTxn, source source.Source) error {
	// Iterating over all as this is a rare operation and it would be costly
	// to always index by source.
	iter, _ := s.fes.All(txn)
	for svc, _, ok := iter.Next(); ok; svc, _, ok = iter.Next() {
		if svc.Source == source {
			if err := s.deleteFrontend(txn, svc); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Services) UpsertBackends(txn ServiceWriteTxn, serviceName loadbalancer.ServiceName, source source.Source, bes ...*loadbalancer.Backend) error {
	if err := s.updateBackends(txn, serviceName, source, bes); err != nil {
		return err
	}

	iter := s.fes.List(txn, FrontendNameIndex.Query(serviceName))
	for fe, _, ok := iter.Next(); ok; fe, _, ok = iter.Next() {
		fe = fe.Clone()
		fe.Status = reconciler.StatusPending()
		fe.BackendRevision = s.bes.Revision(txn)
		if _, _, err := s.fes.Insert(txn, fe); err != nil {
			return err
		}
	}
	return nil
}

func NewServiceNameSet(names ...loadbalancer.ServiceName) container.ImmSet[loadbalancer.ServiceName] {
	return container.NewImmSetFunc(
		loadbalancer.ServiceName.Compare,
		names...,
	)
}

func (s *Services) updateBackends(txn ServiceWriteTxn, serviceName loadbalancer.ServiceName, source source.Source, bes []*loadbalancer.Backend) error {
	for _, bep := range bes {
		var be Backend
		if old, _, ok := s.bes.Get(txn, BackendAddrIndex.Query(bep.L3n4Addr)); ok {
			// Keep the old state, e.g. ID
			be = *old
		} else {
			be.ReferencedBy = NewServiceNameSet(serviceName)
		}
		be.Backend = *bep
		be.Source = source
		be.ReferencedBy = be.ReferencedBy.Insert(serviceName)

		// TODO: Here we should figure out how to merge the two states. E.g. we might learn
		// through health checking that the backend should be inactive, or from k8s that the
		// backend is terminating (which overrides everything).
		// See Service.updateBackendsCacheLocked.
		be.ActualState = bep.State

		if _, _, err := s.bes.Insert(txn, &be); err != nil {
			return err
		}
	}
	return nil
}

func (s *Services) DeleteBackendsBySource(txn ServiceWriteTxn, source source.Source) error {
	// Iterating over all as this is a rare operation and it would be costly
	// to always index by source.
	names := sets.New[loadbalancer.ServiceName]()
	iter, _ := s.bes.All(txn)
	for be, _, ok := iter.Next(); ok; be, _, ok = iter.Next() {
		if be.Source == source {
			names.Insert(be.ReferencedBy.AsSlice()...)
			if _, _, err := s.bes.Delete(txn, be); err != nil {
				return err
			}
		}
	}

	// Bump the backend revision for each of the referenced services to force
	// reconciliation.
	revision := s.bes.Revision(txn)
	for name := range names {
		fe, _, found := s.fes.Get(txn, FrontendNameIndex.Query(name))
		if found {
			fe = fe.Clone()
			fe.BackendRevision = revision
			fe.Status = reconciler.StatusPending()
			if _, _, err := s.fes.Insert(txn, fe); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Services) removeBackendRef(txn ServiceWriteTxn, name loadbalancer.ServiceName, be *Backend) (err error) {
	be, orphan := be.removeRef(name)
	if orphan {
		_, _, err = s.bes.Delete(txn, be)
	} else {
		_, _, err = s.bes.Insert(txn, be)
	}
	return err
}

func (s *Services) ReleaseBackend(txn ServiceWriteTxn, name loadbalancer.ServiceName, addr loadbalancer.L3n4Addr) error {
	be, _, ok := s.bes.Get(txn, BackendAddrIndex.Query(addr))
	if !ok {
		return statedb.ErrObjectNotFound
	}

	if err := s.removeBackendRef(txn, name, be); err != nil {
		return err
	}

	// Bump the backend revision for each of the referenced services to force
	// reconciliation.
	revision := s.bes.Revision(txn)
	iter := s.fes.List(txn, FrontendNameIndex.Query(name))
	for fe, _, ok := iter.Next(); ok; fe, _, ok = iter.Next() {
		fe = fe.Clone()
		fe.BackendRevision = revision
		fe.Status = reconciler.StatusPending()
		if _, _, err := s.fes.Insert(txn, fe); err != nil {
			return err
		}
	}
	return nil
}

func (s *Services) UpdateBackendState(txn ServiceWriteTxn, addr loadbalancer.L3n4Addr, state loadbalancer.BackendState) error {
	be, _, ok := s.bes.Get(txn, BackendAddrIndex.Query(addr))
	if !ok {
		return statedb.ErrObjectNotFound
	}
	be = be.Clone()
	be.State = state
	if _, _, err := s.bes.Insert(txn, be); err != nil {
		return err
	}

	// Bump the backend revision for each of the referenced services to force
	// reconciliation.
	revision := s.bes.Revision(txn)
	for _, name := range be.ReferencedBy.AsSlice() {
		iter := s.fes.List(txn, FrontendNameIndex.Query(name))
		for fe, _, ok := iter.Next(); ok; fe, _, ok = iter.Next() {
			fe = fe.Clone()
			fe.BackendRevision = revision
			fe.Status = reconciler.StatusPending()
			if _, _, err := s.fes.Insert(txn, fe); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Services) DebugDump(txn statedb.ReadTxn, to io.Writer) {
	w := tabwriter.NewWriter(to, 5, 0, 3, ' ', 0)

	fmt.Fprintln(w, "--- Frontends ---")
	fmt.Fprintln(w, strings.Join((*Frontend)(nil).TableHeader(), "\t"))
	iter, _ := s.fes.All(txn)
	for svc, _, ok := iter.Next(); ok; svc, _, ok = iter.Next() {
		fmt.Fprintln(w, strings.Join(svc.TableRow(), "\t"))
	}

	fmt.Fprintln(w, "--- Backends ---")
	fmt.Fprintln(w, strings.Join((*Backend)(nil).TableHeader(), "\t"))
	iterBe, _ := s.bes.All(txn)
	for be, _, ok := iterBe.Next(); ok; be, _, ok = iterBe.Next() {
		fmt.Fprintln(w, strings.Join(be.TableRow(), "\t"))
	}

	w.Flush()
}

// UnsafeRWTable returns the RWTable[*Frontend] for direct unvalidated write access. Only use this
// for reconcilers.
func (s *Services) UnsafeRWTable() statedb.RWTable[*Frontend] {
	return s.fes
}
