// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cmd

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"go.uber.org/fx"

	"github.com/cilium/cilium/api/v1/server"
	"github.com/cilium/cilium/pkg/controller"
	"github.com/cilium/cilium/pkg/datapath/iptables"
	"github.com/cilium/cilium/pkg/egressgateway"
	"github.com/cilium/cilium/pkg/endpoint"
	"github.com/cilium/cilium/pkg/endpointmanager"
	"github.com/cilium/cilium/pkg/ipcache"
	"github.com/cilium/cilium/pkg/k8s/watchers"
	"github.com/cilium/cilium/pkg/maps/lbmap"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/service"
	"github.com/cilium/cilium/pkg/status"
	"github.com/mackerelio/go-osstat/cpu"
)

func runApp() {
	ctx, cancel := context.WithCancel(context.Background())

	appLogger := newAppLogger

	if os.Getenv("CILIUM_PRETTY") != "" {
		appLogger = newPrettyAppLogger
		fmt.Printf("👋 Cilium is initializing...\n")
	}

	t0 := time.Now()

	app := fx.New(
		fx.WithLogger(appLogger),
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
		ipcache.Module,

		fx.Supply(status.DefaultConfig),
		status.Module,

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

		fx.Invoke(registerStatsReporter),

		// The first thing to do when stopping is to cancel the
		// daemon-wide context.
		fx.Invoke(appendOnStop(cancel)),
	)

	if app.Err() != nil {
		log.WithError(app.Err()).Fatal("Failed to initialize daemon")
	}

	if os.Getenv("CILIUM_PRETTY") != "" {
		fmt.Printf("ℹ Initialization complete in %s\n", time.Now().Sub(t0))
		fmt.Printf("🏁 Cilium is starting\n")
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
	IPCache *ipcache.IPCache
}

// daemonLift lifts objects from the 'Daemon' struct into the fx graph
// in order to facilitate the gradual transition into modules.
func daemonLift(d *Daemon) LiftedFromDaemon {
	return LiftedFromDaemon{
		Service: d.svc,
		IPCache: d.ipcache,
	}
}

func optional(flag bool, opts ...fx.Option) fx.Option {
	if flag {
		return fx.Options(opts...)
	}
	return fx.Invoke()
}

func registerStatsReporter(lc fx.Lifecycle, ctx context.Context) {
	if os.Getenv("CILIUM_PRETTY") != "" {
		lc.Append(fx.Hook{
			OnStart: func(context.Context) error {
				go statsReporter(ctx)
				return nil
			},
		})
	}
}

func statsReporter(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var prevMemstats runtime.MemStats
	runtime.ReadMemStats(&prevMemstats)

	cpuBefore, _ := cpu.Get()
	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}

		var memstats runtime.MemStats
		runtime.ReadMemStats(&memstats)

		cpuAfter, _ := cpu.Get()
		total := float64(cpuAfter.Total - cpuBefore.Total)
		user := float64(cpuAfter.User-cpuBefore.User) / total * 100
		system := float64(cpuAfter.System-cpuBefore.System) / total * 100

		controllerStats := controller.GetGlobalStatus()
		var lastErr string
		var lastErrTime time.Time
		for _, s := range controllerStats {
			if s.Status.LastFailureMsg != "" && time.Time(s.Status.LastFailureTimestamp).After(lastErrTime) {
				ago := time.Time(s.Status.LastFailureTimestamp).Sub(time.Now())
				lastErr = fmt.Sprintf("%s: %s (%s ago)", s.Name, s.Status.LastFailureMsg, ago)
			}
		}

		allocRate := memstats.Mallocs - prevMemstats.Mallocs
		fmt.Printf("ℹ %d goroutines, CPU: user %.1f%% system %.1f%%\n", runtime.NumGoroutine(), user, system)
		fmt.Printf("ℹ %dMB in use, %d mallocs/sec\n",
			memstats.Alloc/1024/1024, allocRate)
		fmt.Printf("ℹ %d controllers, last error: %q\n\n", len(controllerStats), lastErr)

		prevMemstats = memstats
		cpuBefore = cpuAfter
	}
}
