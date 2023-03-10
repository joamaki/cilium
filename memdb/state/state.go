package state

import (
	"encoding/json"
	"fmt"

	memdb "github.com/hashicorp/go-memdb"

	"github.com/cilium/cilium/pkg/stream"
)

type State struct {
	stream.Observable[Event]
	db *memdb.MemDB
	r  Reflector

	emit     func(Event)
	complete func(error)
}

func New() (s *State, err error) {
	s = &State{}
	s.db, err = memdb.NewMemDB(schema())
	s.Observable, s.emit, s.complete = stream.Multicast[Event]()
	return
}

func (s *State) SetReflector(r Reflector) {
	s.r = r
}

func (s *State) ToJson() []byte {
	tx := s.db.Txn(false)
	defer tx.Abort()
	out := map[string][]any{}
	for table := range s.db.DBSchema().Tables {
		iter, err := tx.Get(table, "id")
		if err != nil {
			panic(err)
		}
		objs := []any{}
		for obj := iter.Next(); obj != nil; obj = iter.Next() {
			objs = append(objs, obj)
		}
		out[table] = objs
	}
	bs, _ := json.Marshal(out)
	return bs
}

func (s *State) WriteTx() *StateTx {
	tx := s.db.Txn(true)
	tx.TrackChanges()
	return &StateTx{tx, s}
}

// TODO different interface for reads
func (s *State) ReadTx() *StateTx {
	return &StateTx{s.db.Txn(false), nil}
}

func (s *State) Nodes() (Iterator[*Node], error) {
	txn := s.db.Txn(false)
	resIt, err := txn.Get(nodeTable, string(NameIndex))
	if err != nil {
		return nil, fmt.Errorf("node get failed: %w", err)
	}
	return iterator[*Node]{resIt}, nil
}

func (s *State) IPCache() (Iterator[*IPToIdentity], error) {
	txn := s.db.Txn(false)
	resIt, err := txn.Get(ipcacheTable, string(NameIndex))
	if err != nil {
		return nil, fmt.Errorf("identity get failed: %w", err)
	}
	return iterator[*IPToIdentity]{resIt}, nil
}

type Iterator[Obj any] interface {
	// Next returns the next object and true, or zero value and false if iteration
	// has finished.
	Next() (Obj, bool)

	// Invalidated returns a channel that is closed when the results
	// returned by the iterator have changed in the database.
	Invalidated() <-chan struct{}
}

type iterator[Obj any] struct {
	it memdb.ResultIterator
}

func (s iterator[Obj]) Next() (obj Obj, ok bool) {
	if v := s.it.Next(); v != nil {
		// deep-copy? track mutations? etc.
		obj = v.(Obj)
		ok = true
	}
	return
}

func (s iterator[Obj]) Invalidated() <-chan struct{} {
	ch := s.it.WatchCh()
	if ch == nil {
		// Some iterators don't support watching. In these cases we'd
		// need to go via the Observable[Event] instead.
		panic("TODO iterator doesn't support watching")
	}
	return ch
}

type StateTx struct {
	tx *memdb.Txn
	s  *State
}

func (stx *StateTx) Abort()          { stx.tx.Abort() }
func (stx *StateTx) Defer(fn func()) { stx.tx.Defer(fn) }

func (stx *StateTx) Commit() error {
	changes := stx.tx.Changes()
	if stx.s.r != nil {
		if err := stx.s.r.ProcessChanges(changes); err != nil {
			return err
		}
	}
	stx.tx.Commit()
	for _, change := range changes {
		// TODO how much information to include?
		stx.s.emit(Event{table: change.Table})
	}
	return nil
}

func (stx *StateTx) ExtNetworkPolicies() Table[*ExtNetworkPolicy] {
	return &table[*ExtNetworkPolicy]{
		table: extNetworkPolicyTable,
		tx:    stx.tx,
	}
}

func (stx *StateTx) ExtPolicyRules() Table[*ExtPolicyRule] {
	return &table[*ExtPolicyRule]{
		table: extPolicyRuleTable,
		tx:    stx.tx,
	}
}

func (stx *StateTx) Nodes() Table[*Node] {
	return &table[*Node]{
		table: nodeTable,
		tx:    stx.tx,
	}
}

func (stx *StateTx) IPCache() Table[*IPToIdentity] {
	return &table[*IPToIdentity]{
		table: ipcacheTable,
		tx:    stx.tx,
	}
}

func (stx *StateTx) SelectorPolicies() Table[*SelectorPolicy] {
	return &table[*SelectorPolicy]{
		table: selectorPolicyTable,
		tx:    stx.tx,
	}
}

func (stx *StateTx) Endpoints() Table[*Endpoint] {
	return &table[*Endpoint]{
		table: endpointTable,
		tx:    stx.tx,
	}
}

func (stx *StateTx) DatapathEndpoints() Table[*DatapathEndpoint] {
	return &table[*DatapathEndpoint]{
		table: datapathEndpointTable,
		tx:    stx.tx,
	}
}

type Table[Obj any] interface {
	First(Query) (Obj, error)
	Last(Query) (Obj, error)
	Get(Query) (Iterator[Obj], error)

	// TODO LowerBound doesn't provide WatchCh!!!
	LowerBound(Query) (Iterator[Obj], error)

	Insert(obj Obj) error
	Delete(obj Obj) error
	DeleteAll(Query) (n int, err error)

	// TODO prefixed ops

}

type table[Obj any] struct {
	table string
	tx    *memdb.Txn
}

func (t *table[Obj]) Delete(obj Obj) error {
	return t.tx.Delete(t.table, obj)
}

func (t *table[Obj]) DeleteAll(q Query) (int, error) {
	return t.tx.DeleteAll(t.table, string(q.Index), q.Args...)
}

func (t *table[Obj]) First(q Query) (obj Obj, err error) {
	var v any
	v, err = t.tx.First(t.table, string(q.Index), q.Args...)
	if err == nil && v != nil {
		obj = v.(Obj)
	}
	// TODO not found error or zero value Obj is fine?
	return
}

func (t *table[Obj]) Get(q Query) (Iterator[Obj], error) {
	it, err := t.tx.Get(t.table, string(q.Index), q.Args...)
	if err != nil {
		return nil, err
	}
	return iterator[Obj]{it}, nil
}

func (t *table[Obj]) LowerBound(q Query) (Iterator[Obj], error) {
	it, err := t.tx.LowerBound(t.table, string(q.Index), q.Args...)
	if err != nil {
		return nil, err
	}
	return iterator[Obj]{it}, nil
}

func (t *table[Obj]) Insert(obj Obj) error {
	return t.tx.Insert(t.table, obj)
}

func (t *table[Obj]) Last(q Query) (obj Obj, err error) {
	var v any
	v, err = t.tx.Last(t.table, string(q.Index), q.Args...)
	if err == nil && v != nil {
		obj = v.(Obj)
	}
	// TODO not found error or zero value Obj is fine?
	return
}

var _ Table[struct{}] = &table[struct{}]{}
