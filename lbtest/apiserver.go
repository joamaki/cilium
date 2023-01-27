package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"github.com/cilium/cilium/controlplane/servicemanager"
	"github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/gorilla/mux"
)

var apiserverCell = cell.Invoke(registerApiServer)

type apiserverParams struct {
	cell.In

	Lifecycle      hive.Lifecycle
	ServiceManager servicemanager.ServiceManager
}

func registerApiServer(p apiserverParams) {
	mux := mux.NewRouter()
	as := &apiServer{
		p: p,
		s: &http.Server{
			Addr:    ":8080",
			Handler: mux,
		},
		h: p.ServiceManager.NewHandle("api"),
	}
	mux.HandleFunc("/health", as.handleHealth).Methods("GET")
	mux.HandleFunc("/services", as.handleGetServices).Methods("GET")
	mux.HandleFunc("/services/{namespace}/{name}", as.handlePutService).Methods("PUT")
	p.Lifecycle.Append(as)
}

type apiServer struct {
	p apiserverParams
	s *http.Server
	h servicemanager.ServiceHandle
}

var _ hive.HookInterface = &apiServer{}

func (as *apiServer) Start(hive.HookContext) error {
	go as.s.ListenAndServe()
	return nil
}

func (as *apiServer) Stop(hive.HookContext) error {
	return as.s.Close()
}

func (as *apiServer) handleHealth(w http.ResponseWriter, req *http.Request) {
	panic("unimplemented")

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
