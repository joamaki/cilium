package cmd

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/joamaki/genbus"
	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/cilium/cilium/api/v1/server"
	"github.com/cilium/cilium/pkg/aws/eni"
	"github.com/cilium/cilium/pkg/datapath"
	"github.com/cilium/cilium/pkg/datapath/iptables"
	linuxdatapath "github.com/cilium/cilium/pkg/datapath/linux"
	linuxrouting "github.com/cilium/cilium/pkg/datapath/linux/routing"
	"github.com/cilium/cilium/pkg/datapath/maps"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/endpoint"
	"github.com/cilium/cilium/pkg/endpointmanager"
	ipamOption "github.com/cilium/cilium/pkg/ipam/option"
	"github.com/cilium/cilium/pkg/ipcache"
	"github.com/cilium/cilium/pkg/ipmasq"
	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/maps/ctmap/gc"
	monitorAPI "github.com/cilium/cilium/pkg/monitor/api"
	"github.com/cilium/cilium/pkg/node"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/pinit"
	"github.com/cilium/cilium/pkg/recorder"
	wireguard "github.com/cilium/cilium/pkg/wireguard/agent"
)

const (
	startTimeout = 5 * time.Minute
	stopTimeout  = 30 * time.Second
)

var linuxdatapathModule = fx.Module(
	"linux-datapath",

	fx.Supply(linuxdatapath.DatapathConfiguration{
		HostDevice: defaults.HostDevice,
	}),

	wireguard.Module,

	fx.Provide(
		iptables.NewIptablesManager,
		linuxdatapath.NewDatapath,
	),
)

var daemonModule = fx.Module(
	"daemon",

	fx.Provide(
		NewDaemon,
		func(d *Daemon) *ipcache.IPCache {
			return d.ipcache
		},
		func(d *Daemon) datapath.BaseProgramOwner {
			return d
		},
	),

	fx.Invoke(
		preInit,
		validatePostInit,
		endpointManagerInit,
		registerEndpointRestoration,
		registerK8sSynced,
		registerDaemonStart,
	),
)

func runApp(cmd *cobra.Command) {
	// Create a top-level context for the daemon. This is cancelled by the signal handler
	// in cleanup.go and will trigger stop.
	ctx, cancel := context.WithCancel(server.ServerCtx)

	app := fx.New(
		fx.StartTimeout(startTimeout),
		fx.StopTimeout(stopTimeout),
		fx.WithLogger(newAppLogger),

		fx.Supply(
			fx.Annotate(ctx, fx.As(new(context.Context))),
			cancel),

		fx.Supply(cmd),
		fx.Invoke(initEnv),

		fx.Provide(genbus.NewBuilder),

		linuxdatapathModule,
		fx.Provide(newEndpointManager),
		daemonModule,
		nameManagerModule,
		recorder.Module,
		optional(option.Config.EnableIPMasqAgent, ipmasqAgentModule),
		apiServerModule,
		pinit.Module,

		// NOTE: uber/fx executes module's invokes first, and then processes the submodules
		// and their invokes.
		fx.Module(
			"post init",
			fx.Invoke(
				writeDotGraph,
				buildEventBus,
			),
		),
	)

	if app.Err() != nil {
		log.WithError(app.Err()).Fatal("Failed to initialize daemon")
	}

	if option.Config.DotGraphOutputFile != "" {
		log.WithField("file", option.Config.DotGraphOutputFile).Infof("Wrote dot graph and now exiting without starting")
		os.Exit(0)
	}

	startCtx, cancel := context.WithTimeout(context.Background(), app.StartTimeout())
	defer cancel()

	if err := app.Start(startCtx); err != nil {
		if ctx.Err() != nil {
			// Context was cancelled during startup, e.g. due to interrupt. Exit
			// normally in this case.
			os.Exit(0)
		}
		os.Exit(1)
	}

	// Wait until the daemon context is cancelled. This is done via the
	// cleaner on receipt of an interrupt.
	<-ctx.Done()

	stopCtx, cancel := context.WithTimeout(context.Background(), app.StopTimeout())
	defer cancel()

	if err := app.Stop(stopCtx); err != nil {
		log.WithError(err).Fatal("Failed to stop daemon")
	}
}

func optional(flag bool, opts ...fx.Option) fx.Option {
	if flag {
		return fx.Options(opts...)
	}
	return fx.Invoke()
}

func buildEventBus(b *genbus.Builder) (*genbus.EventBus, error) {
	// TODO: add a genbus.Build(b *Builder) (*EventBus, error)
	// TODO: debug log the event graph
	bus, err := b.Build()
	if err == nil {
		bus.PrintGraph()
	}
	return bus, err
}

func preInit() error {
	log.Info("Initializing daemon")
	option.Config.RunMonitorAgent = true

	if err := enableIPForwarding(); err != nil {
		return fmt.Errorf("error when enabling sysctl parameters: %w", err)
	}

	if k8s.IsEnabled() {
		bootstrapStats.k8sInit.Start()
		if err := k8s.Init(option.Config); err != nil {
			return fmt.Errorf("unable to initialize Kubernetes subsystem: %w", err)
		}
		bootstrapStats.k8sInit.End(true)
	}
	return nil
}

func newEndpointManager(ctx context.Context) *endpointmanager.EndpointManager {
	return WithDefaultEndpointManager(ctx, endpoint.CheckHealth)
}

func appendOnStart(lc fx.Lifecycle, f func() error) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			return f()
		},
	})
}

func registerDaemonStart(lc fx.Lifecycle, pi *pinit.ParInit, d *Daemon, restoredEndpoints *endpointRestoreState, rec *recorder.Recorder) {
	pi.Register(pinit.C1, "Daemon startup", func() error {
		return onDaemonStart(d, restoredEndpoints, rec)
	})
}

func registerEndpointRestoration(lc fx.Lifecycle, pi *pinit.ParInit, d *Daemon, restoredEndpoints *endpointRestoreState) error {
	pi.Register(pinit.C0, "Endpoint restore", func() error {
		<-d.initRestore(restoredEndpoints)
		return nil
	})
	return nil
}

func registerK8sSynced(lc fx.Lifecycle, pi *pinit.ParInit, d *Daemon) error {
	pi.Register(pinit.C0, "Wait for K8s cache sync", func() error {
		bootstrapStats.k8sInit.Start()
		if k8s.IsEnabled() {
			// Wait only for certain caches, but not all!
			// (Check Daemon.InitK8sSubsystem() for more info)
			<-d.k8sCachesSynced
		}
		bootstrapStats.k8sInit.End(true)
		return nil
	})
	return nil
}

func onDaemonStart(d *Daemon, restoredEndpoints *endpointRestoreState, rec *recorder.Recorder) error {

	bootstrapStats.enableConntrack.Start()
	log.Info("Starting connection tracking garbage collector")
	gc.Enable(option.Config.EnableIPv4, option.Config.EnableIPv6,
		restoredEndpoints.restored, d.endpointManager)
	bootstrapStats.enableConntrack.End(true)

	restoreWgAgent(d.datapath.WireguardAgent())

	if !option.Config.DryMode {
		go func() {
			runMapSweeper(d.endpointManager)
			restoreCIDRs(d)
		}()
	}

	migrateENI()
	initHealthAndStatus(d)

	err := d.SendNotification(monitorAPI.StartMessage(time.Now()))
	if err != nil {
		return err
		//log.WithError(err).Warn("Failed to send agent start monitor message")
	}

	startNeighborRefresh(d.datapath, d)

	if option.Config.BGPControlPlaneEnabled() {
		log.Info("Initializing BGP Control Plane")
		if err := d.instantiateBGPControlPlane(d.ctx); err != nil {
			return err
			//log.WithError(err).Fatal("Error returned when instantiating BGP control plane")
		}
	}

	writeCNIConfig()
	markNodeReady(d)

	go launchHubble(d, rec)
	storeConfigs()

	return nil
}

func restoreWgAgent(wgAgent datapath.WireguardAgent) {
	if wgAgent != nil {
		agent := wgAgent.(*wireguard.Agent)
		if err := agent.RestoreFinished(); err != nil {
			log.WithError(err).Error("Failed to set up wireguard peers")
		}
	}
}

func endpointManagerInit(lc fx.Lifecycle, em *endpointmanager.EndpointManager, ipcache *ipcache.IPCache, d *Daemon) {
	if em.HostEndpointExists() {
		em.InitHostEndpointLabels(d.ctx)
	} else {
		log.Info("Creating host endpoint")
		if err := em.AddHostEndpoint(
			d.ctx, d, d, ipcache, d.l7Proxy, d.identityAllocator,
			"Create host endpoint", nodeTypes.GetName(),
		); err != nil {
			log.WithError(err).Fatal("Unable to create host endpoint")
		}
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			em.Subscribe(d)
			return nil
		},
		OnStop: func(context.Context) error {
			em.Unsubscribe(d)
			return nil
		},
	})
}

func runMapSweeper(endpointManager *endpointmanager.EndpointManager) {
	if !option.Config.DryMode {
		ms := maps.NewMapSweeper(&EndpointMapManager{
			EndpointManager: endpointManager,
		})
		ms.CollectStaleMapGarbage()
		ms.RemoveDisabledMaps()
	}
}

func restoreCIDRs(d *Daemon) {
	if len(d.restoredCIDRs) > 0 {
		// Release restored CIDR identities after a grace period (default 10
		// minutes).  Any identities actually in use will still exist after
		// this.
		//
		// This grace period is needed when running on an external workload
		// where policy synchronization is not done via k8s. Also in k8s
		// case it is prudent to allow concurrent endpoint regenerations to
		// (re-)allocate the restored identities before we release them.
		time.Sleep(option.Config.IdentityRestoreGracePeriod)
		log.Debugf("Releasing reference counts for %d restored CIDR identities", len(d.restoredCIDRs))

		d.ipcache.ReleaseCIDRIdentitiesByCIDR(d.restoredCIDRs)
		// release the memory held by restored CIDRs
		d.restoredCIDRs = nil
	}
}

func validatePostInit(d *Daemon) {
	// This validation needs to be done outside of the agent until
	// datapath.NodeAddressing is used consistently across the code base.
	log.Info("Validating configured node address ranges")
	if err := node.ValidatePostInit(); err != nil {
		log.WithError(err).Fatal("postinit failed")
	}
}

var ipmasqAgentModule = fx.Module(
	"ipmasq-agent",

	fx.Provide(newIPMasqAgent),
	fx.Invoke(
		func(agent *ipmasq.IPMasqAgent) {},
	),
)

func newIPMasqAgent(lc fx.Lifecycle) (*ipmasq.IPMasqAgent, error) {
	agent, err := ipmasq.NewIPMasqAgent(option.Config.IPMasqAgentConfigPath)
	if agent != nil {
		lc.Append(fx.Hook{
			OnStart: func(context.Context) error {
				agent.Start()
				return nil
			},
			OnStop: func(context.Context) error {
				agent.Stop()
				return nil
			},
		})
	}
	return agent, err
}

func migrateENI() {
	// Migrating the ENI datapath must happen before the API is served to
	// prevent endpoints from being created. It also must be before the health
	// initialization logic which creates the health endpoint, for the same
	// reasons as the API being served. We want to ensure that this migration
	// logic runs before any endpoint creates.
	if option.Config.IPAM == ipamOption.IPAMENI {
		migrated, failed := linuxrouting.NewMigrator(
			&eni.InterfaceDB{},
		).MigrateENIDatapath(option.Config.EgressMultiHomeIPRuleCompat)
		switch {
		case failed == -1:
			// No need to handle this case specifically because it is handled
			// in the call already.
		case migrated >= 0 && failed > 0:
			log.Errorf("Failed to migrate ENI datapath. "+
				"%d endpoints were successfully migrated and %d failed to migrate completely. "+
				"The original datapath is still in-place, however it is recommended to retry the migration.",
				migrated, failed)

		case migrated >= 0 && failed == 0:
			log.Infof("Migration of ENI datapath successful, %d endpoints were migrated and none failed.",
				migrated)
		}
	}

}

func initHealthAndStatus(d *Daemon) {
	bootstrapStats.healthCheck.Start()
	if option.Config.EnableHealthChecking {
		d.initHealth()
	}
	bootstrapStats.healthCheck.End(true)

	d.startStatusCollector()

	metricsErrs := initMetrics()
	go func() {
		err := <-metricsErrs
		if err != nil {
			log.WithError(err).Fatal("Cannot start metrics server")
		}
	}()

	d.startAgentHealthHTTPService()
	if option.Config.KubeProxyReplacementHealthzBindAddr != "" {
		if option.Config.KubeProxyReplacement != option.KubeProxyReplacementDisabled {
			d.startKubeProxyHealthzHTTPService(fmt.Sprintf("%s", option.Config.KubeProxyReplacementHealthzBindAddr))
		}
	}
}

var apiServerModule = fx.Module(
	"api-server",

	fx.Provide(
		instantiateAPI,
		server.NewServer,
	),

	fx.Invoke(
		initAPIServer,
	),
)

func initAPIServer(lc fx.Lifecycle, shutdown fx.Shutdowner, daemonCtx context.Context, srv *server.Server) {
	bootstrapStats.initAPI.Start()
	srv.EnabledListeners = []string{"unix"}
	srv.SocketPath = option.Config.SocketPath
	srv.ReadTimeout = apiTimeout
	srv.WriteTimeout = apiTimeout
	srv.ConfigureAPI()
	bootstrapStats.initAPI.End(true)

	var wg sync.WaitGroup
	wg.Add(1)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				err := srv.Serve()
				if err != nil {
					log.WithError(err).Error("Error returned from non-returning Serve() call")
					shutdown.Shutdown()
				}
				wg.Done()
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			err := srv.Shutdown()
			// Wait for server shutdown to complete.
			wg.Wait()
			return err
		},
	})
}

func startNeighborRefresh(dp datapath.Datapath, d *Daemon) {
	if !dp.Node().NodeNeighDiscoveryEnabled() {
		// Remove all non-GC'ed neighbor entries that might have previously set
		// by a Cilium instance.
		dp.Node().NodeCleanNeighbors(false)
	} else {
		// If we came from an agent upgrade, migrate entries.
		dp.Node().NodeCleanNeighbors(true)
		// Start periodical refresh of the neighbor table from the agent if needed.
		if option.Config.ARPPingRefreshPeriod != 0 && !option.Config.ARPPingKernelManaged {
			d.nodeDiscovery.Manager.StartNeighborRefresh(dp.Node())
		}
	}
}

func writeCNIConfig() {
	log.WithField("bootstrapTime", time.Since(bootstrapTimestamp)).
		Info("Daemon initialization completed")

	if option.Config.WriteCNIConfigurationWhenReady != "" {
		input, err := os.ReadFile(option.Config.ReadCNIConfiguration)
		if err != nil {
			log.WithError(err).Fatal("Unable to read CNI configuration file")
		}

		if err = os.WriteFile(option.Config.WriteCNIConfigurationWhenReady, input, 0644); err != nil {
			log.WithError(err).Fatalf("Unable to write CNI configuration file to %s", option.Config.WriteCNIConfigurationWhenReady)
		} else {
			log.Infof("Wrote CNI configuration file to %s", option.Config.WriteCNIConfigurationWhenReady)
		}
	}
}

func markNodeReady(d *Daemon) {
	if k8s.IsEnabled() {
		bootstrapStats.k8sInit.Start()
		k8s.Client().MarkNodeReady(d.k8sWatcher, nodeTypes.GetName())
		bootstrapStats.k8sInit.End(true)
	}
	bootstrapStats.overall.End(true)
	bootstrapStats.updateMetrics()
}

func storeConfigs() {
	err := option.Config.StoreInFile(option.Config.StateDir)
	if err != nil {
		log.WithError(err).Error("Unable to store Cilium's configuration")
	}

	err = option.StoreViperInFile(option.Config.StateDir)
	if err != nil {
		log.WithError(err).Error("Unable to store Viper's configuration")
	}
}

func writeDotGraph(dot fx.DotGraph) {
	if option.Config.DotGraphOutputFile != "" {
		os.WriteFile(option.Config.DotGraphOutputFile, []byte(dot), 0644)
	}
}
