package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/index"
	"github.com/cilium/cilium/pkg/statedb/reconciler"
	"golang.org/x/sys/unix"
)

func main() {
	hive := hive.New(
		job.Cell,
		reconciler.Cell,

		// StateDB and the /db handler
		statedb.Cell,
		cell.Invoke(func(db *statedb.DB, mux *http.ServeMux) {
			mux.Handle("/db", db)
		}),

		// HTTP server
		cell.Provide(http.NewServeMux),
		cell.Invoke(registerServer),

		// ---------------------------
		cell.Module(
			"bees",
			"Bees",

			cell.Provide(NewBeeTable),
			cell.Invoke(statedb.RegisterTable[*Bee]),
			cell.Invoke(beeHandlers),

			cell.Provide(beeReconcilationConfig),
			cell.Invoke(reconciler.Register[*Bee]),
		),
	)
	hive.Run()
}

func registerServer(lc hive.Lifecycle, mux *http.ServeMux) {
	mux.HandleFunc("/exit", func(http.ResponseWriter, *http.Request) { os.Exit(1) })
	srv := http.Server{Addr: ":8080", Handler: mux}
	lc.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			go srv.ListenAndServe()
			return nil
		},
		OnStop: func(ctx hive.HookContext) error {
			return srv.Shutdown(ctx)
		},
	})
}

type Bee struct {
	Name    string
	Role    string
	Friends []string
	Status  reconciler.Status
}

func (b *Bee) getStatus() reconciler.Status {
	return b.Status
}

func (b *Bee) withStatus(s reconciler.Status) *Bee {
	b2 := *b
	b2.Status = s
	return &b2
}

var (
	BeeNameIndex = statedb.Index[*Bee, string]{
		Name: "name",
		FromObject: func(obj *Bee) index.KeySet {
			return index.NewKeySet(index.String(obj.Name))
		},
		FromKey: index.String,
		Unique:  true,
	}

	BeeRoleIndex = statedb.Index[*Bee, string]{
		Name: "role",
		FromObject: func(obj *Bee) index.KeySet {
			return index.NewKeySet(index.String(obj.Role))
		},
		FromKey: index.String,
		Unique:  false,
	}

	BeeFriendIndex = statedb.Index[*Bee, string]{
		Name: "friends",
		FromObject: func(obj *Bee) index.KeySet {
			return index.StringSlice(obj.Friends)
		},
		FromKey: index.String,
		Unique:  false,
	}
)

func NewBeeTable() (statedb.RWTable[*Bee], error) {
	return statedb.NewTable[*Bee](
		"bees",
		BeeNameIndex,
		BeeRoleIndex,
		BeeFriendIndex,
	)
}

func beeHandlers(db *statedb.DB, mux *http.ServeMux, bees statedb.RWTable[*Bee], health cell.Health) {
	mux.HandleFunc("/bees", func(w http.ResponseWriter, req *http.Request) {
		txn := db.WriteTxn(bees)
		defer txn.Commit()

		beeJson, err := io.ReadAll(req.Body)
		if err == nil {
			var bee Bee
			err = json.Unmarshal(beeJson, &bee)
			if err == nil {
				if req.Method == http.MethodPut {
					bee.Status = reconciler.StatusPending()
					fmt.Fprintf(w, "inserted\n")
				} else if req.Method == http.MethodDelete {
					bee.Status = reconciler.StatusPendingDelete()
					fmt.Fprintf(w, "deleted\n")
				}
				bees.Insert(txn, &bee)
			}
		}
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "error: %s\n", err)
		}
	})

	mux.HandleFunc("/bees/workers", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			flusher := w.(http.Flusher)
			w.WriteHeader(http.StatusOK)
			for {
				iter, watch := bees.Get(db.ReadTxn(), BeeRoleIndex.Query("worker"))

				for bee, _, ok := iter.Next(); ok; bee, _, ok = iter.Next() {
					json, _ := json.Marshal(bee)
					w.Write(json)
					w.Write([]byte{'\n'})
				}
				flusher.Flush()

				select {
				case <-watch:
				case <-req.Context().Done():
					return
				}
			}
		}
	})

	mux.HandleFunc("/bees/changes", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			flusher := w.(http.Flusher)
			w.WriteHeader(http.StatusOK)
			revision := statedb.Revision(0)
			for {
				iter, watch := bees.LowerBound(db.ReadTxn(), statedb.ByRevision[*Bee](revision+1))

				for bee, rev, ok := iter.Next(); ok; bee, rev, ok = iter.Next() {
					revision = rev

					json, _ := json.Marshal(bee)
					w.Write(json)
					w.Write([]byte{'\n'})
				}
				flusher.Flush()

				select {
				case <-watch:
				case <-req.Context().Done():
					return
				}
			}
		}
	})

	mux.HandleFunc("/bees/friended/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet {
			name := path.Base(req.URL.Path)
			iter, _ := bees.Get(db.ReadTxn(), BeeFriendIndex.Query(name))

			for bee, _, ok := iter.Next(); ok; bee, _, ok = iter.Next() {
				json, _ := json.Marshal(bee)
				w.Write(json)
				w.Write([]byte{'\n'})
			}
		}
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		b, err := json.Marshal(health.All())
		if err != nil {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(200)
			w.Write(b)
		}
	})
}

type beeOps struct{}

// Delete implements reconciler.Operations.
func (beeOps) Delete(ctx context.Context, txn statedb.ReadTxn, bee *Bee) error {
	err := os.Remove(bee.Name + ".json")
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

// Prune implements reconciler.Operations.
func (beeOps) Prune(ctx context.Context, txn statedb.ReadTxn, iter statedb.Iterator[*Bee]) error {
	return nil
}

// Update implements reconciler.Operations.
func (beeOps) Update(ctx context.Context, txn statedb.ReadTxn, bee *Bee, changed *bool) error {
	json, err := json.Marshal(bee)
	if err != nil {
		return err
	}
	return os.WriteFile(bee.Name+".json", json, 0644)
}

var _ reconciler.Operations[*Bee] = beeOps{}

func beeReconcilationConfig() reconciler.Config[*Bee] {
	return reconciler.Config[*Bee]{
		FullReconcilationInterval: 10 * time.Second,
		RetryBackoffMinDuration:   100 * time.Second,
		RetryBackoffMaxDuration:   time.Second,
		IncrementalRoundSize:      100,
		GetObjectStatus:           (*Bee).getStatus,
		WithObjectStatus:          (*Bee).withStatus,
		RateLimiter:               nil,
		Operations:                beeOps{},
		BatchOperations:           nil,
	}
}
