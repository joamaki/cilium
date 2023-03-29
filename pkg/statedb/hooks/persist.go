package hooks

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	memdb "github.com/hashicorp/go-memdb"

	"github.com/cilium/cilium/pkg/statedb"
)

type DummyPersistHook[Obj any] struct {
	path              string
	primaryKeyIndexer memdb.SingleIndexer
}

func NewDummyPersistHook[Obj any](pkeyIndexSchema *memdb.IndexSchema, path string) statedb.CommitHook {
	os.MkdirAll(path, 0700)

	pkeyIndexer, ok := pkeyIndexSchema.Indexer.(memdb.SingleIndexer)
	if !ok {
		panic("IndexSchema's Indexer does not implement FromObject")
	}
	return &DummyPersistHook[Obj]{path, pkeyIndexer}
}

func (h *DummyPersistHook[Obj]) extractPrimaryKey(change memdb.Change) (string, error) {
	var obj any
	if change.Deleted() {
		obj = change.Before
	} else {
		obj = change.After
	}
	_, bs, err := h.primaryKeyIndexer.FromObject(obj)
	return hex.EncodeToString(bs), err
}

// Restore implements statedb.CommitHook
func (h *DummyPersistHook[Obj]) Restore(table statedb.TableName, txn *memdb.Txn) error {
	n := 0

	err := fs.WalkDir(
		os.DirFS(h.path),
		".",
		func(path string, d fs.DirEntry, err error) error {
			if d.IsDir() {
				return nil
			}
			bs, err := os.ReadFile(filepath.Join(h.path, path))
			if err != nil {
				return err
			}
			var obj Obj
			if err := json.Unmarshal(bs, &obj); err != nil {
				return err
			}
			n++
			return txn.Insert(string(table), obj)
		},
	)
	if err == nil {
		fmt.Printf("Restored %d objects\n", n)
	} else {
		fmt.Printf("Restore failed: %s\n", err)
	}
	return err
}

// Commit implements statedb.CommitHook
func (h *DummyPersistHook[Obj]) Commit(changes []memdb.Change) error {
	fmt.Printf("DummyPersistHook: Committing %d changes\n", len(changes))
	for _, change := range changes {
		pkey, err := h.extractPrimaryKey(change)
		if err != nil {
			return err
		}
		objPath := path.Join(h.path, string(pkey))

		if change.Deleted() {
			fmt.Printf("Deleting %q\n", objPath)
			os.Remove(objPath)
		} else {
			// FIXME idempotency/rollback of changes on errors, e.g.
			// atomic replace of the target with rename after all written.
			// Or better yet, implement on top of e.g. pebble.
			// FIXME use more efficient marshalling schema. Protobuf?
			data, err := json.Marshal(change.After)
			if err != nil {
				return err
			}
			fmt.Printf("Updating %q\n", objPath)
			err = os.WriteFile(objPath, data, 0700)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
