// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cmd

import (
	"context"
	"time"

	"go.uber.org/fx"

	"github.com/cilium/cilium/api/v1/server"
	"github.com/cilium/cilium/api/v1/server/restapi"
	"github.com/cilium/cilium/pkg/option"
)

func runApp() {
	ctx, cancel := context.WithCancel(context.Background())
	app := fx.New(
		fx.WithLogger(newAppLogger),
		fx.Supply(fx.Annotate(ctx, fx.As(new(context.Context)))),
		cleanerModule,

		fx.Provide(daemonModule),
		gopsModule,
		server.Module,
		fx.Provide(provideAPI),
		fx.Invoke(configureAPI),

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

func provideAPI(d *Daemon) *restapi.CiliumAPIAPI {
	return d.instantiateAPI()
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
