package cmd

import (
	"net/http"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"

	"github.com/go-openapi/loads"

	"github.com/cilium/cilium/api/v1/server"
	"github.com/cilium/cilium/api/v1/server/restapi"
	"github.com/cilium/cilium/pkg/api"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
)

var apiServerCell = cell.Module(
	"cilium-api-server",
	"Serves the Cilium API",

	cell.Provide(newAPIServer),

	hive.AppendHooks[*APIServer](),
)

type APIServer struct {
	server http.Server

	mux            *http.ServeMux
	grpcServer     *grpc.Server
	swaggerHandler http.Handler
}

func (s *APIServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.ProtoMajor == 2 && strings.HasPrefix(
		req.Header.Get("Content-Type"), "application/grpc") {
		s.grpcServer.ServeHTTP(w, req)
	} else {
		s.swaggerHandler.ServeHTTP(w, req)
	}
}

func (s *APIServer) Start(hive.HookContext) error {
	go s.server.ListenAndServe()
	return nil
}

func (s *APIServer) Stop(ctx hive.HookContext) error {
	return s.server.Shutdown(ctx)
}

type apiServerParams struct {
	cell.In

	Services []api.GRPCService `group:"grpc-services"`
}

func newAPIServer(p apiServerParams) (*APIServer, error) {
	s := &APIServer{
		mux:        http.NewServeMux(),
		grpcServer: grpc.NewServer(),
	}

	for _, svc := range p.Services {
		s.grpcServer.RegisterService(svc.Service, svc.Impl)
	}

	s.mux.Handle("/", s.grpcServer)

	spec, err := loads.Analyzed(server.SwaggerJSON, "")
	if err != nil {
		return nil, err
	}
	api := restapi.NewCiliumAPIAPI(spec)
	s.swaggerHandler = api.Serve(nil)

	s.server.Addr = ":8456"
	s.server.Handler = h2c.NewHandler(s, &http2.Server{})

	return s, nil
}
