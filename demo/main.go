package main

import (
	"log"
	"time"

	"github.com/spf13/cobra"

	"github.com/cilium/cilium/daemon/tables"
	"github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/source"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/reconciler"
)

var svcs *tables.Services

var Hive = hive.New(
	job.Cell,
	client.Cell,
	statedb.Cell,
	reconciler.Cell,

	tables.ServicesCell,
	tables.K8sReflectorCell,

	/*
		cell.Invoke(func(s *tables.Services) {
			go demo(s)
		}),*/
)

var cmd = &cobra.Command{
	Use: "example",
	Run: func(_ *cobra.Command, args []string) {
		if err := Hive.Run(); err != nil {
			log.Fatal(err)
		}
	},
}

func main() {
	// Register all configuration flags in the hive to the command
	Hive.RegisterFlags(cmd.Flags())

	// Add the "hive" sub-command for inspecting the hive
	cmd.AddCommand(Hive.Command())

	// And finally execute the command to parse the command-line flags and
	// run the hive
	cmd.Execute()
}

func demo(s *tables.Services) {
	name := loadbalancer.ServiceName{
		Namespace: "foo",
		Name:      "bar",
	}

	txn := s.WriteTxn()
	s.UpsertService(
		txn,
		name,
		tables.ServiceParams{
			L3n4Addr:        *loadbalancer.NewL3n4Addr(loadbalancer.TCP, types.MustParseAddrCluster("1.2.3.4"), 12345, loadbalancer.ScopeExternal),
			Type:            loadbalancer.SVCTypeClusterIP,
			Labels:          map[string]labels.Label{},
			Source:          source.Kubernetes,
			NatPolicy:       loadbalancer.SVCNatPolicyNone,
			ExtPolicy:       loadbalancer.SVCTrafficPolicyNone,
			IntPolicy:       loadbalancer.SVCTrafficPolicyNone,
			SessionAffinity: nil,
			HealthCheck:     nil,
		},
	)
	s.UpsertService(
		txn,
		name,
		tables.ServiceParams{
			L3n4Addr:        *loadbalancer.NewL3n4Addr(loadbalancer.TCP, types.MustParseAddrCluster("0.0.0.0"), 40404, loadbalancer.ScopeExternal),
			Type:            loadbalancer.SVCTypeNodePort,
			Labels:          map[string]labels.Label{},
			Source:          source.Kubernetes,
			NatPolicy:       loadbalancer.SVCNatPolicyNone,
			ExtPolicy:       loadbalancer.SVCTrafficPolicyNone,
			IntPolicy:       loadbalancer.SVCTrafficPolicyNone,
			SessionAffinity: nil,
			HealthCheck:     nil,
		},
	)
	backend1 := *loadbalancer.NewL3n4Addr(loadbalancer.TCP, types.MustParseAddrCluster("4.3.2.1"), 54321, loadbalancer.ScopeExternal)
	s.UpsertBackends(
		txn,
		name,
		tables.BackendParams{
			L3n4Addr:  backend1,
			Source:    source.Kubernetes,
			PortName:  "foo",
			NodeName:  "bar",
			Weight:    123,
			State:     loadbalancer.BackendStateActive,
			Preferred: false,
		},
	)
	backend2 := *loadbalancer.NewL3n4Addr(loadbalancer.TCP, types.MustParseAddrCluster("4.3.2.2"), 54322, loadbalancer.ScopeExternal)
	s.UpsertBackends(
		txn,
		name,
		tables.BackendParams{
			L3n4Addr:  backend2,
			Source:    source.Kubernetes,
			PortName:  "foo",
			NodeName:  "bar",
			Weight:    123,
			State:     loadbalancer.BackendStateTerminating,
			Preferred: false,
		},
	)
	txn.Commit()

	time.Sleep(time.Second)

	txn = s.WriteTxn()
	err := s.DeleteBackend(txn, name, backend1)
	if err != nil {
		panic(err)
	}
	txn.Commit()
}
