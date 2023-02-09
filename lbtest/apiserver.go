package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"github.com/cilium/cilium/lbtest/controlplane/services"
	"github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

var apiserverCell = cell.Module(
	"lbtest-apiserver",
	"Simple JSON API server for lbtest",

	cell.Invoke(registerApiServer),
)

type apiserverParams struct {
	cell.In

	Lifecycle      hive.Lifecycle
	Shutdowner     hive.Shutdowner
	ServiceManager services.ServiceManager
	StatusProvider *cell.StatusProvider
	StatusReporter cell.StatusReporter
	Log            logrus.FieldLogger
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
		h: p.ServiceManager.NewHandle("api"),
	}
	as.h.Synchronized()

	mux.HandleFunc("/health", as.handleHealth).Methods("GET")
	mux.HandleFunc("/health/stream", as.handleHealthStream).Methods("GET")
	mux.HandleFunc("/services", as.handleGetServices).Methods("GET")
	mux.HandleFunc("/services/{namespace}/{name}", as.handlePutService).Methods("PUT")
	p.Lifecycle.Append(as)
}

type apiServer struct {
	p apiserverParams
	s *http.Server
	h services.ServiceHandle

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
	as.p.StatusReporter.OK("Listening on " + as.s.Addr)
	return nil
}

func (as *apiServer) Stop(ctx hive.HookContext) error {
	defer as.p.StatusReporter.Down("Stopped")
	as.h.Close()
	as.cancel()
	return as.s.Shutdown(ctx)
}

func (as *apiServer) handleHealth(w http.ResponseWriter, req *http.Request) {
	all := as.p.StatusProvider.All()
	bs, err := json.Marshal(all)
	if err == nil {
		w.Write(bs)
	} else {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (as *apiServer) handleHealthStream(w http.ResponseWriter, req *http.Request) {
	f := w.(http.Flusher)
	w.WriteHeader(200)
	for _, s := range as.p.StatusProvider.All() {
		fmt.Fprintln(w, s.String())
		f.Flush()
	}

	// Merge the request context and the api server context.
	ctx, cancel := context.WithCancel(req.Context())
	go func() {
		select {
		case <-as.ctx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	for s := range as.p.StatusProvider.Stream(ctx) {
		fmt.Fprintln(w, s.String())
		f.Flush()
	}

	if as.ctx.Err() != nil {
		fmt.Fprintln(w, "Shutting down.")
		f.Flush()
	}
}

func (as *apiServer) handleGetServices(w http.ResponseWriter, req *http.Request) {
	all := as.p.ServiceManager.All()
	bs, err := json.Marshal(all)
	if err == nil {
		w.Write(bs)
	} else {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type putServiceRequest struct {
	Kind     string // LoadBalancer or NodePort
	Frontend string // only for LoadBalancer
	Port     uint16
	Backends []string
}

func (r *putServiceRequest) toFE(name loadbalancer.ServiceName) (loadbalancer.FE, error) {
	switch r.Kind {
	case "LoadBalancer":
		addr, err := types.ParseAddrCluster(r.Frontend)
		if err != nil {
			return nil, err
		}
		l3n4 := loadbalancer.NewL3n4Addr("tcp", addr, r.Port, loadbalancer.ScopeExternal)
		return &loadbalancer.FELoadBalancer{
			CommonFE: loadbalancer.CommonFE{
				Name:             name,
				ExtTrafficPolicy: loadbalancer.SVCTrafficPolicyCluster,
				NatPolicy:        loadbalancer.SVCNatPolicyNone,
				SessionAffinity:  false,
			},
			L3n4Addr: *l3n4,
		}, nil

	case "NodePort":
		return &loadbalancer.FENodePort{
			CommonFE: loadbalancer.CommonFE{
				Name:             name,
				ExtTrafficPolicy: loadbalancer.SVCTrafficPolicyCluster,
				NatPolicy:        loadbalancer.SVCNatPolicyNone,
			},
			L4Addr: loadbalancer.L4Addr{Protocol: loadbalancer.TCP, Port: r.Port},
			Scope:  loadbalancer.ScopeExternal,
		}, nil
	default:
		return nil, fmt.Errorf("unknown kind: %q", r.Kind)
	}

}

func (r *putServiceRequest) toBackends(name loadbalancer.ServiceName) ([]*loadbalancer.Backend, error) {
	bes := []*loadbalancer.Backend{}
	for _, beAddr := range r.Backends {
		addrS, portS, found := strings.Cut(beAddr, ":")
		if !found {
			return nil, fmt.Errorf("invalid address %q", beAddr)
		}
		addr, err := types.ParseAddrCluster(addrS)
		if err != nil {
			return nil, err
		}
		port, err := strconv.ParseInt(portS, 10, 32)
		if err != nil {
			return nil, err
		}
		l3n4 := loadbalancer.NewL3n4Addr("tcp", addr, uint16(port), loadbalancer.ScopeExternal)
		bes = append(bes, &loadbalancer.Backend{
			Weight:    loadbalancer.DefaultBackendWeight,
			L3n4Addr:  *l3n4,
			State:     loadbalancer.BackendStateActive,
			Preferred: true,
		})
	}
	return bes, nil
}

func (as *apiServer) handlePutService(w http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)

	bs, err := ioutil.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	var psRequest putServiceRequest
	if err := json.Unmarshal(bs, &psRequest); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := loadbalancer.ServiceName{
		Authority: loadbalancer.AuthorityAPI,
		Namespace: vars["namespace"],
		Name:      vars["name"],
	}
	fe, err := psRequest.toFE(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	bes, err := psRequest.toBackends(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	as.h.UpsertFrontend(fe)
	as.h.UpsertBackends(name, bes...)
}
