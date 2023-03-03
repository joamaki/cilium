// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

// Ensure build fails on versions of Go that are not supported by Cilium.
// This build tag should be kept in sync with the version specified in go.mod.
//go:build go1.19

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"

	operatorWatchers "github.com/cilium/cilium/operator/watchers"
	"github.com/cilium/cilium/pkg/clustermesh"
	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/gops"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/inctimer"
	"github.com/cilium/cilium/pkg/ipcache"
	"github.com/cilium/cilium/pkg/k8s"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	k8sClient "github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/informer"
	"github.com/cilium/cilium/pkg/k8s/resource"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	"github.com/cilium/cilium/pkg/k8s/synced"
	"github.com/cilium/cilium/pkg/k8s/types"
	"github.com/cilium/cilium/pkg/kvstore"
	"github.com/cilium/cilium/pkg/kvstore/store"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
	nodeStore "github.com/cilium/cilium/pkg/node/store"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/pprof"
)

type configuration struct {
	clusterName      string
	clusterID        uint32
	serviceProxyName string
}

func (c configuration) LocalClusterName() string {
	return c.clusterName
}

func (c configuration) LocalClusterID() uint32 {
	return c.clusterID
}

func (c configuration) K8sServiceProxyNameValue() string {
	return c.serviceProxyName
}

var (
	log = logging.DefaultLogger.WithField(logfields.LogSubsys, "clustermesh-apiserver")

	vp       *viper.Viper
	rootHive *hive.Hive

	rootCmd = &cobra.Command{
		Use:   "clustermesh-apiserver",
		Short: "Run the ClusterMesh apiserver",
		Run: func(cmd *cobra.Command, args []string) {
			if err := rootHive.Run(); err != nil {
				log.Fatal(err)
			}
		},
		PreRun: func(cmd *cobra.Command, args []string) {
			option.Config.Populate(vp)
			if option.Config.Debug {
				log.Logger.SetLevel(logrus.DebugLevel)
			}
			option.LogRegisteredOptions(vp, log)
		},
	}

	mockFile string
	cfg      configuration

	ciliumNodeStore *store.SharedStore
)

func init() {
	rootHive = hive.New(
		gops.Cell(defaults.GopsPortApiserver),
		k8sClient.Cell,
		k8s.SharedResourcesCell,
		kvstoreCell,

		healthAPIServerCell,
		identitySynchronizerCell,
		cell.Invoke(registerHooks),
	)
	rootHive.RegisterFlags(rootCmd.Flags())
	rootCmd.AddCommand(rootHive.Command())
	vp = rootHive.Viper()
}

func registerHooks(lc hive.Lifecycle, clientset k8sClient.Clientset, backend kvstore.BackendOperations, services resource.Resource[*slim_corev1.Service]) error {
	lc.Append(hive.Hook{
		OnStart: func(ctx hive.HookContext) error {
			if !clientset.IsEnabled() {
				return errors.New("Kubernetes client not configured, cannot continue.")
			}
			startServer(ctx, clientset, backend, services)
			return nil
		},
	})
	return nil
}

func readMockFile(backend kvstore.BackendOperations, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("unable to open file %s: %s", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.Contains(line, "\"CiliumIdentity\""):
			var identity ciliumv2.CiliumIdentity
			err := json.Unmarshal([]byte(line), &identity)
			if err != nil {
				log.WithError(err).WithField("line", line).Warning("Unable to unmarshal CiliumIdentity")
			} else {
				updateIdentity(backend, &identity)
			}
		case strings.Contains(line, "\"CiliumNode\""):
			var node ciliumv2.CiliumNode
			err = json.Unmarshal([]byte(line), &node)
			if err != nil {
				log.WithError(err).WithField("line", line).Warning("Unable to unmarshal CiliumNode")
			} else {
				updateNode(&node)
			}
		case strings.Contains(line, "\"CiliumEndpoint\""):
			var endpoint types.CiliumEndpoint
			err = json.Unmarshal([]byte(line), &endpoint)
			if err != nil {
				log.WithError(err).WithField("line", line).Warning("Unable to unmarshal CiliumEndpoint")
			} else {
				updateEndpoint(nil, &endpoint)
			}
		case strings.Contains(line, "\"Service\""):
			var service slim_corev1.Service
			err = json.Unmarshal([]byte(line), &service)
			if err != nil {
				log.WithError(err).WithField("line", line).Warning("Unable to unmarshal Service")
			} else {
				operatorWatchers.K8sSvcCache.UpdateService(&service, nil)
			}
		case strings.Contains(line, "\"Endpoints\""):
			var endpoints slim_corev1.Endpoints
			err = json.Unmarshal([]byte(line), &endpoints)
			if err != nil {
				log.WithError(err).WithField("line", line).Warning("Unable to unmarshal Endpoints")
			} else {
				operatorWatchers.K8sSvcCache.UpdateEndpoints(&endpoints, nil)
			}
		default:
			log.Warningf("Unknown line in mockfile %s: %s", path, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func runApiserver() error {
	flags := rootCmd.Flags()
	flags.BoolP(option.DebugArg, "D", false, "Enable debugging mode")
	option.BindEnv(vp, option.DebugArg)

	flags.Duration(option.CRDWaitTimeout, 5*time.Minute, "Cilium will exit if CRDs are not available within this duration upon startup")
	option.BindEnv(vp, option.CRDWaitTimeout)

	flags.String(option.IdentityAllocationMode, option.IdentityAllocationModeCRD, "Method to use for identity allocation")
	option.BindEnv(vp, option.IdentityAllocationMode)

	flags.Uint32Var(&cfg.clusterID, option.ClusterIDName, 0, "Cluster ID")
	option.BindEnv(vp, option.ClusterIDName)

	flags.StringVar(&cfg.clusterName, option.ClusterName, "default", "Cluster name")
	option.BindEnv(vp, option.ClusterName)

	flags.StringVar(&mockFile, "mock-file", "", "Read from mock file")

	flags.Duration(option.KVstoreConnectivityTimeout, defaults.KVstoreConnectivityTimeout, "Time after which an incomplete kvstore operation  is considered failed")
	option.BindEnv(vp, option.KVstoreConnectivityTimeout)

	flags.Duration(option.KVstoreLeaseTTL, defaults.KVstoreLeaseTTL, "Time-to-live for the KVstore lease.")
	flags.MarkHidden(option.KVstoreLeaseTTL)
	option.BindEnv(vp, option.KVstoreLeaseTTL)

	flags.Duration(option.KVstorePeriodicSync, defaults.KVstorePeriodicSync, "Periodic KVstore synchronization interval")
	option.BindEnv(vp, option.KVstorePeriodicSync)

	flags.Var(option.NewNamedMapOptions(option.KVStoreOpt, &option.Config.KVStoreOpt, nil),
		option.KVStoreOpt, "Key-value store options e.g. etcd.address=127.0.0.1:4001")
	option.BindEnv(vp, option.KVStoreOpt)

	flags.StringVar(&cfg.serviceProxyName, option.K8sServiceProxyName, "", "Value of K8s service-proxy-name label for which Cilium handles the services (empty = all services without service.kubernetes.io/service-proxy-name label)")
	option.BindEnv(vp, option.K8sServiceProxyName)

	flags.Duration(option.AllocatorListTimeoutName, defaults.AllocatorListTimeout, "Timeout for listing allocator state before exiting")
	option.BindEnv(vp, option.AllocatorListTimeoutName)

	flags.Bool(option.EnableWellKnownIdentities, defaults.EnableWellKnownIdentities, "Enable well-known identities for known Kubernetes components")
	option.BindEnv(vp, option.EnableWellKnownIdentities)

	flags.Bool(option.K8sEnableEndpointSlice, defaults.K8sEnableEndpointSlice, "Enable support of Kubernetes EndpointSlice")
	option.BindEnv(vp, option.K8sEnableEndpointSlice)

	flags.Bool(option.PProf, false, "Enable serving the pprof debugging API")
	option.BindEnv(vp, option.PProf)

	flags.String(option.PProfAddress, defaults.PprofAddressAPIServer, "Address that pprof listens on")
	option.BindEnv(vp, option.PProfAddress)

	flags.Int(option.PProfPort, defaults.PprofPortAPIServer, "Port that pprof listens on")
	option.BindEnv(vp, option.PProfPort)

	vp.BindPFlags(flags)

	if err := rootCmd.Execute(); err != nil {
		return err
	}

	return nil
}

func main() {
	if err := runApiserver(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func parseLabelArrayFromMap(base map[string]string) labels.LabelArray {
	array := make(labels.LabelArray, 0, len(base))
	for sourceAndKey, value := range base {
		array = append(array, labels.NewLabel(sourceAndKey, value, ""))
	}
	return array.Sort()
}

type nodeStub struct {
	cluster string
	name    string
}

func (n *nodeStub) GetKeyName() string {
	return nodeTypes.GetKeyNodeName(n.cluster, n.name)
}

func updateNode(obj interface{}) {
	if ciliumNode, ok := obj.(*ciliumv2.CiliumNode); ok {
		n := nodeTypes.ParseCiliumNode(ciliumNode)
		n.Cluster = cfg.clusterName
		n.ClusterID = cfg.clusterID
		if err := ciliumNodeStore.UpdateLocalKeySync(context.Background(), &n); err != nil {
			log.WithError(err).Warning("Unable to insert node into etcd")
		} else {
			log.Infof("Inserted node into etcd: %v", n)
		}
	} else {
		log.Warningf("Unknown CiliumNode object type %s received: %+v", reflect.TypeOf(obj), obj)
	}
}

func deleteNode(obj interface{}) {
	n, ok := obj.(*ciliumv2.CiliumNode)
	if ok {
		n := nodeStub{
			cluster: cfg.clusterName,
			name:    n.Name,
		}
		ciliumNodeStore.DeleteLocalKey(context.Background(), &n)
	} else {
		log.Warningf("Unknown CiliumNode object type %s received: %+v", reflect.TypeOf(obj), obj)
	}
}

func synchronizeNodes(nodes k8s.AllCiliumNodesResource) {
	for ev := range nodes.Events(context.TODO()) {
		switch ev.Kind {
		case resource.Upsert:
			updateNode(ev.Object)
		case resource.Delete:
			deleteNode(ev.Object)
		}
		ev.Done(nil)
	}

	/*
		_, ciliumNodeInformer := informer.NewInformer(
			cache.NewListWatchFromClient(clientset.CiliumV2().RESTClient(),
				"ciliumnodes", k8sv1.NamespaceAll, fields.Everything()),
			&ciliumv2.CiliumNode{},
			0,
			cache.ResourceEventHandlerFuncs{
				AddFunc: updateNode,
				UpdateFunc: func(_, newObj interface{}) {
					updateNode(newObj)
				},
				DeleteFunc: func(obj interface{}) {
					deletedObj, ok := obj.(cache.DeletedFinalStateUnknown)
					if ok {
						deleteNode(deletedObj.Obj)
					} else {
						deleteNode(obj)
					}
				},
			},
			k8s.ConvertToCiliumNode,
		)

		go ciliumNodeInformer.Run(wait.NeverStop)*/
}

func updateEndpoint(oldEp, newEp *types.CiliumEndpoint) {
	var ipsAdded []string
	if n := newEp.Networking; n != nil {
		for _, address := range n.Addressing {
			for _, ip := range []string{address.IPV4, address.IPV6} {
				if ip == "" {
					continue
				}

				keyPath := path.Join(ipcache.IPIdentitiesPath, ipcache.DefaultAddressSpace, ip)
				entry := identity.IPIdentityPair{
					IP:           net.ParseIP(ip),
					Metadata:     "",
					HostIP:       net.ParseIP(n.NodeIP),
					K8sNamespace: newEp.Namespace,
					K8sPodName:   newEp.Name,
				}

				if newEp.Identity != nil {
					entry.ID = identity.NumericIdentity(newEp.Identity.ID)
				}

				if newEp.Encryption != nil {
					entry.Key = uint8(newEp.Encryption.Key)
				}

				marshaledEntry, err := json.Marshal(entry)
				if err != nil {
					log.WithError(err).Warningf("Unable to JSON marshal entry %#v", entry)
					continue
				}

				_, err = kvstore.Client().UpdateIfDifferent(context.Background(), keyPath, marshaledEntry, true)
				if err != nil {
					log.WithError(err).Warningf("Unable to update endpoint %s in etcd", keyPath)
				} else {
					ipsAdded = append(ipsAdded, ip)
					log.Infof("Inserted endpoint into etcd: %v", entry)
				}
			}
		}
	}

	// Delete the old endpoint IPs from the KVStore in case the endpoint
	// changed its IP addresses.
	if oldEp == nil {
		return
	}
	oldNet := oldEp.Networking
	if oldNet == nil {
		return
	}
	for _, address := range oldNet.Addressing {
		for _, oldIP := range []string{address.IPV4, address.IPV6} {
			var found bool
			for _, newIP := range ipsAdded {
				if newIP == oldIP {
					found = true
					break
				}
			}
			if !found {
				// Delete the old IPs from the kvstore:
				keyPath := path.Join(ipcache.IPIdentitiesPath, ipcache.DefaultAddressSpace, oldIP)
				if err := kvstore.Client().Delete(context.Background(), keyPath); err != nil {
					log.WithError(err).
						WithFields(logrus.Fields{
							"path": keyPath,
						}).Warningf("Unable to delete endpoint in etcd")
				}
			}
		}
	}
}

func deleteEndpoint(obj interface{}) {
	e, ok := obj.(*types.CiliumEndpoint)
	if !ok {
		log.Warningf("Unknown CiliumEndpoint object type %T received: %+v", obj, obj)
		return
	}

	if n := e.Networking; n != nil {
		for _, address := range n.Addressing {
			for _, ip := range []string{address.IPV4, address.IPV6} {
				if ip == "" {
					continue
				}

				keyPath := path.Join(ipcache.IPIdentitiesPath, ipcache.DefaultAddressSpace, ip)
				if err := kvstore.Client().Delete(context.Background(), keyPath); err != nil {
					log.WithError(err).Warningf("Unable to delete endpoint %s in etcd", keyPath)
				}
			}
		}
	}
}

func synchronizeCiliumEndpoints(clientset k8sClient.Clientset) {
	_, ciliumEndpointsInformer := informer.NewInformer(
		cache.NewListWatchFromClient(clientset.CiliumV2().RESTClient(),
			"ciliumendpoints", k8sv1.NamespaceAll, fields.Everything()),
		&ciliumv2.CiliumEndpoint{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				e, ok := obj.(*types.CiliumEndpoint)
				if !ok {
					log.Warningf("Unknown CiliumEndpoint object type %T received: %+v", obj, obj)
					return
				}
				updateEndpoint(nil, e)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				oldEp, ok := oldObj.(*types.CiliumEndpoint)
				if !ok {
					log.Warningf("Unknown CiliumEndpoint object type %T received: %+v", oldObj, oldObj)
					return
				}
				newEp, ok := newObj.(*types.CiliumEndpoint)
				if !ok {
					log.Warningf("Unknown CiliumEndpoint object type %T received: %+v", newObj, newObj)
					return
				}
				updateEndpoint(oldEp, newEp)
			},
			DeleteFunc: func(obj interface{}) {
				deletedObj, ok := obj.(cache.DeletedFinalStateUnknown)
				if ok {
					deleteEndpoint(deletedObj.Obj)
				} else {
					deleteEndpoint(obj)
				}
			},
		},
		k8s.ConvertToCiliumEndpoint,
	)

	go ciliumEndpointsInformer.Run(wait.NeverStop)
}

var kvstoreCell = cell.Provide(
	func() kvstore.BackendOperations {
		var err error
		if err = kvstore.Setup(context.Background(), "etcd", option.Config.KVStoreOpt, nil); err != nil {
			log.WithError(err).Fatal("Unable to connect to etcd")
		}

		return kvstore.Client()
	})

func startServer(startCtx hive.HookContext, clientset k8sClient.Clientset, backend kvstore.BackendOperations, services resource.Resource[*slim_corev1.Service]) {
	log.WithFields(logrus.Fields{
		"cluster-name": cfg.clusterName,
		"cluster-id":   cfg.clusterID,
	}).Info("Starting clustermesh-apiserver...")

	if mockFile == "" {
		synced.SyncCRDs(startCtx, clientset, synced.AllCRDResourceNames(), &synced.Resources{}, &synced.APIGroups{})
	}

	mgr := NewVMManager(clientset)

	config := cmtypes.CiliumClusterConfig{
		ID: cfg.clusterID,
	}

	if err := clustermesh.SetClusterConfig(cfg.clusterName, &config, backend); err != nil {
		log.WithError(err).Fatal("Unable to set local cluster config on kvstore")
	}

	_, err := store.JoinSharedStore(store.Configuration{
		Prefix:     nodeStore.NodeRegisterStorePrefix,
		KeyCreator: nodeStore.RegisterKeyCreator,
		Observer:   mgr,
	})
	if err != nil {
		log.WithError(err).Fatal("Unable to set up node register store in etcd")
	}

	ciliumNodeStore, err = store.JoinSharedStore(store.Configuration{
		Prefix:     nodeStore.NodeStorePrefix,
		KeyCreator: nodeStore.KeyCreator,
	})
	if err != nil {
		log.WithError(err).Fatal("Unable to set up node store in etcd")
	}

	if mockFile != "" {
		if err := readMockFile(backend, mockFile); err != nil {
			log.WithError(err).Fatal("Unable to read mock file")
		}
	} else {
		synchronizeCiliumEndpoints(clientset)
		operatorWatchers.StartSynchronizingServices(context.Background(), &sync.WaitGroup{}, clientset, services, false, cfg)
	}

	go func() {
		timer, timerDone := inctimer.New()
		defer timerDone()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), defaults.LockLeaseTTL)
			err := backend.Update(ctx, kvstore.HeartbeatPath, []byte(time.Now().Format(time.RFC3339)), true)
			if err != nil {
				log.WithError(err).Warning("Unable to update heartbeat key")
			}
			cancel()
			<-timer.After(kvstore.HeartbeatWriteInterval)
		}
	}()

	if option.Config.PProf {
		pprof.Enable(option.Config.PProfAddress, option.Config.PProfPort)
	}

	log.Info("Initialization complete")
}
