package main

import (
	"context"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var serverCell = cell.Module(
	"server",
	"Simple JSON API server",

	cell.Invoke(registerApiServer),
)

type apiserverParams struct {
	cell.In

	Lifecycle  hive.Lifecycle
	Shutdowner hive.Shutdowner
	State      *state.State
	Log        logrus.FieldLogger
}

func registerApiServer(p apiserverParams) {
	mux := mux.NewRouter()
	ctx, cancel := context.WithCancel(context.Background())
	as := &apiServer{
		ctx:    ctx,
		cancel: cancel,
		p:      p,
		s: &http.Server{
			Addr:    ":8080",
			Handler: mux,
		},
	}
	mux.HandleFunc("/state", as.handleGetState).Methods("GET")
	p.Lifecycle.Append(as)
}

type apiServer struct {
	p apiserverParams
	s *http.Server

	ctx    context.Context
	cancel context.CancelFunc
}

var _ hive.HookInterface = &apiServer{}

func (as *apiServer) Start(hive.HookContext) error {
	go func() {
		err := as.s.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			as.p.Shutdowner.Shutdown(hive.ShutdownWithError(err))
		}
	}()
	as.p.Log.WithField("addr", as.s.Addr).Info("API server listening")
	return nil
}

func (as *apiServer) Stop(ctx hive.HookContext) error {
	as.cancel()
	return as.s.Shutdown(ctx)
}

func (as *apiServer) handleGetState(w http.ResponseWriter, req *http.Request) {
	w.Write(as.p.State.ToJson())
}
