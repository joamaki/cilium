package state

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func newMeta(namespace string, name string) Meta {
	return Meta{
		ID:        uuid.New().String(),
		Name:      name,
		Namespace: namespace,
		Labels:    nil,
	}
}

func TestState(t *testing.T) {
	state, err := New()
	assert.NoError(t, err)
	state.SetReflector(&exampleReflector{})

	assertGetFooBar := func(tx StateTx) {
		it, err := tx.Nodes().Get(ByName("foo", "bar"))
		if assert.NoError(t, err) {
			obj, ok := it.Next()
			if assert.True(t, ok, "GetByName iterator should return object") {
				assert.Equal(t, "bar", obj.Name)
			}

		}
	}

	// Create the foo/bar and baz/quux nodes.
	{
		tx := state.WriteTx()
		nodes := tx.Nodes()
		err = nodes.Insert(&Node{
			Meta:     newMeta("foo", "bar"),
			Identity: 1234,
		})
		assert.NoError(t, err)

		err = nodes.Insert(&Node{
			Meta:     newMeta("baz", "quux"),
			Identity: 1234,
		})
		assert.NoError(t, err)

		assertGetFooBar(tx)
		tx.Commit()
	}
	// Check that it's been committed.
	assertGetFooBar(state.ReadTx())

	// Check that we can iterate over all nodes.
	it, err := state.Nodes()
	if assert.NoError(t, err) {
		n := 0
		for obj, ok := it.Next(); ok; obj, ok = it.Next() {
			n++
			fmt.Printf("obj: %+v\n", obj)
		}
		assert.EqualValues(t, 2, n)
	}

	// Check that we can iterate by namespace
	it, err = state.ReadTx().Nodes().Get(ByNamespace("baz"))
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

	tx2 := state.WriteTx()
	err = tx2.Nodes().Insert(
		&Node{
			Meta:     newMeta("baz", "flup"),
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
