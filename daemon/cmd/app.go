// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cmd

import (
	"context"
	"time"

	"go.uber.org/fx"

	"github.com/cilium/cilium/api/v1/server"
	"github.com/cilium/cilium/pkg/datapath/iptables"
	"github.com/cilium/cilium/pkg/egressgateway"
	"github.com/cilium/cilium/pkg/endpoint"
	"github.com/cilium/cilium/pkg/endpointmanager"
	"github.com/cilium/cilium/pkg/k8s/watchers"
	"github.com/cilium/cilium/pkg/maps/lbmap"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/service"
)

func runApp() {
	ctx, cancel := context.WithCancel(context.Background())
	app := fx.New(
		fx.WithLogger(newAppLogger),
		fx.Supply(fx.Annotate(ctx, fx.As(new(context.Context)))),
		fx.Supply(option.Config),

		gopsModule,
		cleanerModule,
		healthzModule,
		cachesSyncedModule,

		fx.Provide(daemonModule),
		fx.Provide(daemonLift),

		fx.Provide(apiHandlerBridge),
		fx.Provide(apiProvider),
		fx.Invoke(configureAPI),

		server.Module,
		lbmap.Module,
		service.Module,
		optional(option.Config.EnableIPv4EgressGateway, egressgateway.Module),

		iptables.Module,

		fx.Supply(
			fx.Annotate(
				&watchers.EndpointSynchronizer{},
				fx.As(new(endpointmanager.EndpointResourceSynchronizer))),
		),
		optional(
			option.Config.EndpointGCInterval > 0,
			fx.Supply(&endpointmanager.PeriodicEndpointGCParams{
				Interval:    option.Config.EndpointGCInterval,
				CheckHealth: endpoint.CheckHealth,
			}),
		),
		endpointmanager.Module,

		// The first thing to do when stopping is to cancel the
		// daemon-wide context.
		fx.Invoke(appendOnStop(cancel)),
	)

	if app.Err() != nil {
		log.WithError(app.Err()).Fatal("Failed to initialize daemon")
	}

	app.Run()
}

func appendOnStop(onStop func()) func(fx.Lifecycle) {
	return func(lc fx.Lifecycle) {
		lc.Append(fx.Hook{
			OnStop: func(context.Context) error {
				onStop()
				return nil
			},
		})
	}
}

func configureAPI(srv *server.Server) {
	bootstrapStats.initAPI.Start()
	srv.EnabledListeners = []string{"unix"}
	srv.SocketPath = option.Config.SocketPath
	srv.ReadTimeout = apiTimeout
	srv.WriteTimeout = apiTimeout
	srv.GracefulTimeout = time.Second
	srv.ConfigureAPI()
	bootstrapStats.initAPI.End(true)
}

type LiftedFromDaemon struct {
	fx.Out

	Service *service.Service // For pkg/service/handlers.go.
}

// daemonLift lifts objects from the 'Daemon' struct into the fx graph
// in order to facilitate the gradual transition into modules.
func daemonLift(d *Daemon) LiftedFromDaemon {
	return LiftedFromDaemon{
		Service: d.svc,
	}
}

func optional(flag bool, opts ...fx.Option) fx.Option {
	if flag {
		return fx.Options(opts...)
	}
	return fx.Invoke()
}
