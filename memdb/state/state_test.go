package state

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/cilium/cilium/memdb/state/structs"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func newMeta(namespace string, name string) ExtMeta {
	return ExtMeta{
		ID:        uuid.New().String(),
		Name:      name,
		Namespace: namespace,
		Labels:    nil,
	}
}

func TestState(t *testing.T) {
	hive := hive.New(
		cell.Provide(func() *testing.T { return t }),
		Cell,
		cell.Invoke(runTest),
	)
	hive.Start(context.TODO())
	hive.Stop(context.TODO())
}

type testParams struct {
	cell.In

	State *State
	Nodes Table[*Node]
}

func testStateInvoke(t *testing.T, p testParams) {
	state, err := New()
	assert.NoError(t, err)
	state.SetReflector(&exampleReflector{})

	assertGetFooBar := func(tx ReadTransaction) {
		it, err := p.Nodes.Read(tx).Get(ByName("foo", "bar"))
		if assert.NoError(t, err) {
			obj, ok := it.Next()
			if assert.True(t, ok, "GetByName iterator should return object") {
				assert.Equal(t, "bar", obj.Name)
			}

		}
	}

	// Create the foo/bar and baz/quux nodes.
	{
		tx := state.Write()
		nodes := p.Nodes.Modify(tx)
		err = nodes.Insert(&Node{
			ExtMeta:  newMeta("foo", "bar"),
			Identity: 1234,
		})
		assert.NoError(t, err)

		err = nodes.Insert(&Node{
			ExtMeta:  newMeta("baz", "quux"),
			Identity: 1234,
		})
		assert.NoError(t, err)

		assertGetFooBar(tx)
		tx.Commit()
	}
	// Check that it's been committed.
	assertGetFooBar(state.Read())

	// Check that we can iterate over all nodes.
	rtx := state.Read()
	it, err := p.Nodes.Read(rtx).Get(All)
	if assert.NoError(t, err) {
		n := 0
		for obj, ok := it.Next(); ok; obj, ok = it.Next() {
			n++
			fmt.Printf("obj: %+v\n", obj)
		}
		assert.EqualValues(t, 2, n)
	}

	// Check that we can iterate by namespace
	it, err = p.Nodes.Read(rtx).Get(ByNamespace("baz"))
	if assert.NoError(t, err) {
		obj, ok := it.Next()
		if assert.True(t, ok) {
			assert.Equal(t, "quux", obj.Name)
		}
		obj, ok = it.Next()
		assert.False(t, ok)
		assert.Nil(t, obj)
	}

	// Check that we're notified when something in specific namespace changes
	ch := it.Invalidated()
	select {
	case <-ch:
		t.Errorf("expected Invalidated() channel to block!")
	default:
	}

	tx2 := state.Write()
	err = p.Nodes.Modify(tx2).Insert(
		&Node{
			ExtMeta:  newMeta("baz", "flup"),
			Identity: 1234,
		})
	assert.NoError(t, err)
	tx2.Commit()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Errorf("expected Invalidated() channel to be closed!")
	}
}
