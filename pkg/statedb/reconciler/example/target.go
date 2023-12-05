package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/reconciler"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
)

// Target writes [Memo]s to disk.
type Target struct {
	log       logrus.FieldLogger
	directory string
}

// Delete implements reconciler.Target.
func (t *Target) Delete(ctx context.Context, txn statedb.ReadTxn, memo *Memo) error {
	filename := path.Join(t.directory, memo.Name)
	t.log.Infof("Delete(%s)", filename)
	return os.Remove(filename)
}

// Prune implements reconciler.Target.
func (t *Target) Prune(ctx context.Context, txn statedb.ReadTxn, iter statedb.Iterator[*Memo]) error {
	t.log.Info("Pruning...")

	expected := sets.New[string]()
	for memo, _, ok := iter.Next(); ok; memo, _, ok = iter.Next() {
		expected.Insert(memo.Name)
	}

	// Find unexpected files
	unexpected := sets.New[string]()
	if entries, err := os.ReadDir(t.directory); err != nil {
		return err
	} else {
		for _, entry := range entries {
			if !expected.Has(entry.Name()) {
				unexpected.Insert(entry.Name())
			}
		}
	}

	// ... and remove them.
	var errs []error
	for name := range unexpected {
		filename := path.Join(t.directory, name)
		t.log.Infof("Prune(%s)", filename)
		err := os.Remove(filename)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Update implements reconciler.Target.
func (t *Target) Update(ctx context.Context, txn statedb.ReadTxn, memo *Memo) (changed bool, err error) {
	filename := path.Join(t.directory, memo.Name)

	// Read the old file to figure out if it had changed.
	// The 'changed' boolean is used by full reconciliation to keep track of when the target
	// has gone out-of-sync (e.g. there has been some outside influence to it).
	old, err := os.ReadFile(filename)
	if err == nil && bytes.Equal(old, []byte(memo.Content)) {
		// Nothing to do.
		return false, nil
	}
	err = os.WriteFile(filename, []byte(memo.Content), 0644)
	t.log.Infof("Update(%s)", filename)
	return true, err
}

var _ reconciler.Target[*Memo] = &Target{}

// Start implements hive.HookInterface.
func (t *Target) Start(hive.HookContext) error {
	return os.MkdirAll(t.directory, 0755)
}

// Stop implements hive.HookInterface.
func (*Target) Stop(hive.HookContext) error {
	return nil
}

var _ hive.HookInterface = &Target{}

func NewTarget(lc hive.Lifecycle, log logrus.FieldLogger, cfg Config) reconciler.Target[*Memo] {
	t := &Target{directory: cfg.Directory, log: log}
	lc.Append(t)
	return t
}
