package fsm

import (
	"fmt"

	"golang.org/x/exp/slices"

	"github.com/cilium/cilium/pkg/statedb"
)

type StateObject[Obj any, PrimaryKey any] interface {
	statedb.ObjectConstraints[Obj]
	PrimaryKey() PrimaryKey
}

type fsm[Obj StateObject[Obj, PrimaryKey], PrimaryKey any] struct {
	table       statedb.Table[Obj]
	stateField  string // The name of the field in 'Obj' holding fsm.CurrentState
	transitions StateTransitions[PrimaryKey]
	pkeyQuery   func(Obj) statedb.Query
	// TODO primary key SingleIndexer
}

func (f *fsm[Obj, PKey]) getState(obj Obj) State {
	return "" // FIXME extract 'stateField'
}

func (f *fsm[Obj, PKey]) step(obj Obj) error {
	t, ok := f.transitions[f.getState(obj)]
	if !ok {
		return fmt.Errorf("unspecified state transition")
	}
	ctx := &sctx[Obj, PKey]{next: t.Next, table: f.table, q: f.pkeyQuery(obj)}
	t.Process(obj.PrimaryKey(), ctx)
	return nil
}

type sctx[Obj StateObject[Obj, PrimaryKey], PrimaryKey any] struct {
	next  []State
	table statedb.Table[Obj]
	q     statedb.Query
}

func (c *sctx[Obj, PKey]) Done(txn statedb.WriteTransaction, newState State) error {
	if !slices.Contains(c.next, newState) {
		return fmt.Errorf("invalid state transition")
	}
	w := c.table.Writer(txn)
	// Grab the latest version of the object
	o, err := w.First(c.q)
	if err != nil {
		// Invalid query.
		return err
	}
	o = o.DeepCopy()
	// TODO with reflect set the current state field to new state.
	return nil
}

type State string

// CurrentState holds the current state, but does not export a way to modify it.
// All state changes are done via StateContext that is only available when processing
// a state transition.
type CurrentState struct {
	state State
}

func (c CurrentState) Is(s State) bool {
	return c.state == s
}

type StateContext interface {
	// Done finishes the state transition. May error out
	// if the state transition is invalid.
	Done(statedb.WriteTransaction, State) error
}

type StateTransition[PrimaryKey any] struct {
	Process func(PrimaryKey, StateContext)
	Next    []State
}

type StateTransitions[PrimaryKey any] map[State]StateTransition[PrimaryKey]

type Ex struct {
	ID    statedb.UUID
	State CurrentState
}

var (
	ExStateInit     State = "init"
	ExStateBubbling State = "bubbling"
	ExStateStale    State = "stale"
)

var example = StateTransitions[statedb.UUID]{
	ExStateInit: StateTransition[statedb.UUID]{
		Process: dummyHandler,
		Next:    []State{ExStateBubbling, ExStateStale},
	},
}

func dummyHandler(id statedb.UUID, ctx StateContext) {
}
