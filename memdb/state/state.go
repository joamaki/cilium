package state

import (
	"encoding/json"

	memdb "github.com/hashicorp/go-memdb"

	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/stream"
)

type State struct {
	stream.Observable[Event]
	db *memdb.MemDB
	r  Reflector

	emit     func(Event)
	complete func(error)
}

type stateParams struct {
	cell.In

	TableSchemas []*memdb.TableSchema `group:"state-table-schemas"`
}

func New(p stateParams) (s *State, err error) {
	s = &State{}
	s.db, err = memdb.NewMemDB(schema(p.TableSchemas))
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

type stateTransaction struct {
	s   *State
	txn *memdb.Txn
}

func (s *stateTransaction) getTxn() *memdb.Txn {
	return s.txn
}

func (s *State) Write() WriteTransaction {
	txn := s.db.Txn(true)
	txn.TrackChanges()
	return &stateTransaction{s: s, txn: txn}
}

func (s *State) Read() ReadTransaction {
	return &stateTransaction{s: nil, txn: s.db.Txn(false)}
}

func (stx *stateTransaction) Commit() error {
	changes := stx.txn.Changes()
	if stx.s.r != nil {
		if err := stx.s.r.ProcessChanges(changes); err != nil {
			return err
		}
	}
	stx.txn.Commit()
	for _, change := range changes {
		// TODO how much information to include?
		stx.s.emit(Event{table: change.Table})
	}
	return nil
}

func (stx *stateTransaction) Abort()          { stx.txn.Abort() }
func (stx *stateTransaction) Defer(fn func()) { stx.txn.Defer(fn) }

type WatchableIterator[Obj any] interface {
	Iterator[Obj]

	// Invalidated returns a channel that is closed when the results
	// returned by the iterator have changed in the database.
	Invalidated() <-chan struct{}
}

type Iterator[Obj any] interface {
	// Next returns the next object and true, or zero value and false if iteration
	// has finished.
	Next() (Obj, bool)
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
		// Some iterators don't support watching. The normal Iterator[] type
		// should be used and not the WatchableIterator[].
		panic("Internal error: WatchCH() returned nil. This query should return plain Iterator[] instead?")
	}
	return ch
}
