package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

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
	"github.com/cilium/cilium/pkg/ipmasq"
	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/maps/ctmap/gc"
	monitorAPI "github.com/cilium/cilium/pkg/monitor/api"
	"github.com/cilium/cilium/pkg/node"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/nodediscovery"
	"github.com/cilium/cilium/pkg/option"
	wireguard "github.com/cilium/cilium/pkg/wireguard/agent"
	"go.uber.org/fx"
)

var sweeperModule = fx.Module(
	"map-sweeper",
	fx.Provide(
		newEndpointMapManager,
		maps.NewMapSweeper,
	),
	fx.Invoke(startMapSweeper),
)

// TODO: bootstrapStats? Or derive it from fx?

var app = fx.New(
	fx.Provide(
		newDaemonContextAndCancel,
		newIptablesManager,
		newLinuxDatapath,
		newEndpointManager,
		NewDaemon,
	),
	fx.Invoke(
		enableIPForwarding,
		initK8s,
		validate,
		startCTGC,
		waitForK8sCachesSynced,
		restoreEndpoints,
	),

	wireguard.Module, // TODO: when should RestoreFinished be called?
	sweeperModule,

	fx.Invoke(
		endpointManagerInit,
		startIPMasqAgent,
		migrateENI,
		initHealthAndStatus,
		startNeighborRefresh,
		writeCNIConfig,
		markNodeReady,
		launchHubble,
		storeConfigs,
	),
)

func runApp() {
	app.Run()
}

func initK8s() {
	if k8s.IsEnabled() {
		if err := k8s.Init(option.Config); err != nil {
			log.WithError(err).Fatal("Unable to initialize Kubernetes subsystem")
		}
	}
}

func newDaemonContextAndCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func newEndpointManager(ctx context.Context) *endpointmanager.EndpointManager {
	return WithDefaultEndpointManager(ctx, endpoint.CheckHealth)
}

func newIptablesManager() *iptables.IptablesManager {
	mngr := &iptables.IptablesManager{}
	mngr.Init()
	return mngr
}

func newLinuxDatapath(iptablesManager *iptables.IptablesManager, wgAgent *wireguard.Agent) datapath.Datapath {
	datapathConfig := linuxdatapath.DatapathConfiguration{
		HostDevice: defaults.HostDevice,
	}
	return linuxdatapath.NewDatapath(datapathConfig, iptablesManager, wgAgent)
}

func restoreEndpoints(d *Daemon, state *endpointRestoreState) {
	restoreComplete := d.initRestore(state)
	if restoreComplete != nil {
		<-restoreComplete
	}

	if !option.Config.DryMode {
		// TODO: Where should this go?
		go d.dnsNameManager.CompleteBootstrap()
	}
}

func endpointManagerInit(lc fx.Lifecycle, em *endpointmanager.EndpointManager, d *Daemon) {
	if em.HostEndpointExists() {
		em.InitHostEndpointLabels(d.ctx)
	} else {
		log.Info("Creating host endpoint")
		if err := em.AddHostEndpoint(
			d.ctx, d, d, d.ipcache, d.l7Proxy, d.identityAllocator,
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

func startMapSweeper(ms *maps.MapSweeper) {
	if option.Config.DryMode {
		return
	}
	go func() {
		ms.CollectStaleMapGarbage()
		ms.RemoveDisabledMaps()
	}()
}

func newEndpointMapManager(em *endpointmanager.EndpointManager) *EndpointMapManager {
	return &EndpointMapManager{em}
}

func validate(d *Daemon) {
	// This validation needs to be done outside of the agent until
	// datapath.NodeAddressing is used consistently across the code base.
	log.Info("Validating configured node address ranges")
	if err := node.ValidatePostInit(); err != nil {
		log.WithError(err).Fatal("postinit failed")
	}
}

func waitForK8sCachesSynced(d *Daemon) {
	if k8s.IsEnabled() {
		// Wait only for certain caches, but not all!
		// (Check Daemon.InitK8sSubsystem() for more info)
		<-d.k8sCachesSynced
	}
}

func startCTGC(d *Daemon, restoredEndpoints *endpointRestoreState) {
	log.Info("Starting connection tracking garbage collector")
	gc.Enable(option.Config.EnableIPv4, option.Config.EnableIPv6,
		restoredEndpoints.restored, d.endpointManager)
}

func startIPMasqAgent() {
	// TODO: should have as constructor, but here because NewIPMasqAgent
	// installs a fsnotify watcher. Also we probably should use all the
	// option.Config.Enable* flags to construct the list of invoke functions.
	// This way we can have a comprehensive static list of constructors, but
	// configuration driven list of invoke functions.
	if option.Config.EnableIPMasqAgent {
		ipmasqAgent, err := ipmasq.NewIPMasqAgent(option.Config.IPMasqAgentConfigPath)
		if err != nil {
			log.WithError(err).Fatal("Failed to create ip-masq-agent")
		}
		ipmasqAgent.Start()
	}
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
	if option.Config.EnableHealthChecking {
		d.initHealth()
	}
	d.startStatusCollector()

	d.startAgentHealthHTTPService()
	if option.Config.KubeProxyReplacementHealthzBindAddr != "" {
		if option.Config.KubeProxyReplacement != option.KubeProxyReplacementDisabled {
			d.startKubeProxyHealthzHTTPService(fmt.Sprintf("%s", option.Config.KubeProxyReplacementHealthzBindAddr))
		}
	}
}

func startAPIServer(lc fx.Lifecycle, d *Daemon) {
	// TODO: Server should provide a Module, and this logic
	// should be in a constructor and not invoke fn.

	srv := server.NewServer(d.instantiateAPI())
	srv.EnabledListeners = []string{"unix"}
	srv.SocketPath = option.Config.SocketPath
	srv.ReadTimeout = apiTimeout
	srv.WriteTimeout = apiTimeout
	srv.ConfigureAPI()

	err := d.SendNotification(monitorAPI.StartMessage(time.Now()))
	if err != nil {
		log.WithError(err).Warn("Failed to send agent start monitor message")
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// FIXME handle Serve errors
			go srv.Serve()
			return nil
		},
		OnStop: func(context.Context) error {
			return srv.Shutdown()
		},
	})
}

func startNeighborRefresh(dp datapath.Datapath, ndisc *nodediscovery.NodeDiscovery) {
	if !dp.Node().NodeNeighDiscoveryEnabled() {
		// Remove all non-GC'ed neighbor entries that might have previously set
		// by a Cilium instance.
		dp.Node().NodeCleanNeighbors(false)
	} else {
		// If we came from an agent upgrade, migrate entries.
		dp.Node().NodeCleanNeighbors(true)
		// Start periodical refresh of the neighbor table from the agent if needed.
		if option.Config.ARPPingRefreshPeriod != 0 && !option.Config.ARPPingKernelManaged {
			ndisc.Manager.StartNeighborRefresh(dp.Node())
		}
	}
}

func writeCNIConfig() {
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

func markNodeReady(d *Daemon /* TODO K8sWatcher */) {
	if k8s.IsEnabled() {
		k8s.Client().MarkNodeReady(d.k8sWatcher, nodeTypes.GetName())
	}
}

func launchHubble(d *Daemon /* TODO Hubble */) {
	go d.launchHubble()
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

func daemonStart(d *Daemon) {

}
