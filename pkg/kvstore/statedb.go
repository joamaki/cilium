// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package kvstore

import (
	"context"
	"fmt"

	"github.com/cilium/cilium/pkg/time"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"
	"github.com/cilium/stream"
)

func RegisterReflector[Obj any](name string, jobGroup job.Group, db *statedb.DB, targetTable statedb.RWTable[Obj], cfg ReflectorConfig[Obj]) error {
	// Register initializer that marks when the table has been initially populated,
	// e.g. the initial "List" has concluded.
	r := &kvstoreReflector[Obj]{
		ReflectorConfig: cfg.withDefaults(),
		db:              db,
		table:           targetTable,
		name:            name,
	}
	wtxn := db.WriteTxn(targetTable)
	r.initDone = targetTable.RegisterInitializer(wtxn, name)
	wtxn.Commit()

	jobGroup.Add(job.OneShot(
		fmt.Sprintf("kvstore-reflector-%s", name),
		r.run))

	return nil
}

type ReflectorConfig[Obj any] struct {
	// Maximum number of objects to commit in one transaction. Uses default if left zero.
	// This does not apply to the initial listing which is committed in one go.
	BufferSize int

	// The amount of time to wait for the buffer to fill. Uses default if left zero.
	BufferWaitTime time.Duration

	Events stream.Observable[TypedKeyValueEvent[Obj]]
}

const (
	// DefaultBufferSize is the maximum number of objects to commit to the table in one write transaction.
	DefaultBufferSize = 10000

	// DefaultBufferWaitTime is the amount of time to wait to fill the buffer before committing objects.
	// 10000 * 50ms => 200k objects per second throughput limit.
	DefaultBufferWaitTime = 50 * time.Millisecond
)

// withDefaults fills in unset fields with default values.
func (cfg ReflectorConfig[Obj]) withDefaults() ReflectorConfig[Obj] {
	if cfg.BufferSize == 0 {
		cfg.BufferSize = DefaultBufferSize
	}
	if cfg.BufferWaitTime == 0 {
		cfg.BufferWaitTime = DefaultBufferWaitTime
	}
	return cfg
}

type kvstoreReflector[Obj any] struct {
	ReflectorConfig[Obj]

	name     string
	initDone func(statedb.WriteTxn)
	db       *statedb.DB
	table    statedb.RWTable[Obj]
}

func (r *kvstoreReflector[Obj]) run(ctx context.Context, health cell.Health) error {
	type entry struct {
		deleted bool
		key     string
		obj     Obj
	}
	type buffer struct {
		synced  bool
		entries map[string]entry
	}
	bufferSize := r.BufferSize
	waitTime := r.BufferWaitTime
	table := r.table

	// Construct a stream of objects, buffered into chunks every [waitTime] period
	// and then committed.
	// This reduces the number of write transactions required and thus the number of times
	// readers get woken up, which results in much better overall throughput.
	src := stream.Buffer(
		r.Events,
		bufferSize,
		waitTime,

		// Buffer the events into a map, coalescing them by key.
		func(buf *buffer, ev TypedKeyValueEvent[Obj]) *buffer {
			if buf == nil {
				buf = &buffer{
					entries: make(map[string]entry, bufferSize),
				}
			}

			buf.synced = buf.synced || ev.Typ == EventTypeListDone

			var entry entry
			entry.obj = ev.Value
			entry.deleted = ev.Typ == EventTypeDelete
			entry.key = ev.Key
			buf.entries[ev.Key] = entry
			return buf
		},
	)

	commitBuffer := func(buf *buffer) {
		numUpserted, numDeleted := 0, 0

		txn := r.db.WriteTxn(table)
		for _, entry := range buf.entries {
			if !entry.deleted {
				numUpserted++
				table.Insert(txn, entry.obj)
			} else {
				numDeleted++
				table.Delete(txn, entry.obj)
			}
		}

		numTotal := table.NumObjects(txn)

		if buf.synced {
			// Mark the table as initialized. Internally this has a sync.Once
			// so safe to call multiple times.
			r.initDone(txn)
		}

		txn.Commit()

		health.OK(fmt.Sprintf("%d inserted, %d deleted, %d total", numUpserted, numDeleted, numTotal))
	}

	errs := make(chan error)
	src.Observe(
		ctx,
		commitBuffer,
		func(err error) {
			errs <- err
			close(errs)
		},
	)
	return <-errs
}
