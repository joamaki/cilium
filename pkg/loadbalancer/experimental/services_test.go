// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/reconciler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cilium/cilium/pkg/cidr"
	"github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/container"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	"github.com/cilium/cilium/pkg/source"
)

type servicesParams struct {
	cell.In

	DB       *statedb.DB
	Services *experimental.Services

	ServiceTable statedb.Table[*experimental.Service]
	BackendTable statedb.Table[*experimental.Backend]
}

func servicesFixture(t testing.TB) (p servicesParams) {
	log := hivetest.Logger(t, hivetest.LogLevel(slog.LevelError))

	h := hive.New(
		experimental.ServicesCell,

		cell.Invoke(func(p_ servicesParams) { p = p_ }),
	)

	hive.AddConfigOverride(h, func(cfg *experimental.Config) {
		cfg.EnableNewServices = true
	})

	require.NoError(t, h.Start(log, context.TODO()))
	t.Cleanup(func() {
		h.Stop(log, context.TODO())
	})
	return p
}

func intToAddr(i int) types.AddrCluster {
	var addr [4]byte
	binary.BigEndian.PutUint32(addr[:], 0x0100_0000+uint32(i))
	addrCluster, _ := types.AddrClusterFromIP(addr[:])
	return addrCluster
}

func TestServices_Service_UpsertDelete(t *testing.T) {
	p := servicesFixture(t)
	name := loadbalancer.ServiceName{Namespace: "test", Name: "test1"}
	addrCluster := intToAddr(1)
	frontend := *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster, 12345, loadbalancer.ScopeExternal)

	// Add a dump of the state if the test fails. Note that we abort
	// the delete write transactions so they're not visible via this.
	t.Cleanup(func() {
		if t.Failed() {
			p.Services.DebugDump(p.DB.ReadTxn(), os.Stdout)
		}
	})

	// UpsertService
	{
		wtxn := p.Services.WriteTxn()

		created, err := p.Services.UpsertService(
			wtxn,
			&experimental.Service{
				Name:   name,
				Source: source.Kubernetes,
				Frontends: experimental.NewFrontendsMap(
					experimental.Frontend{
						Address: frontend,
						Type:    loadbalancer.SVCTypeClusterIP,
						Props:   experimental.EmptyProps,
					},
				),
			},
		)
		require.True(t, created, "Service created")
		require.NoError(t, err, "UpsertService")
		wtxn.Commit()
	}

	// Lookup
	{
		txn := p.DB.ReadTxn()
		require.Equal(t, 1, p.ServiceTable.NumObjects(txn))
		svc, _, found := p.ServiceTable.Get(txn, experimental.ServiceFrontendsIndex.Query(frontend))
		if assert.True(t, found, "Service not found by addr") {
			assert.NotNil(t, svc)
			assert.Equal(t, name, svc.Name, "Service name not equal")
			assert.Equal(t, reconciler.StatusKindPending, svc.Status.Kind, "Marked pending")
		}

		svc, _, found = p.ServiceTable.Get(txn, experimental.ServiceNameIndex.Query(name))
		if assert.True(t, found, "Service not found by name") {
			assert.NotNil(t, svc)
			assert.Equal(t, name, svc.Name, "Service name not equal")
			assert.Equal(t, reconciler.StatusKindPending, svc.Status.Kind, "Marked pending")
		}
	}

	// Deletion by name
	{
		wtxn := p.Services.WriteTxn()
		require.Equal(t, 1, p.ServiceTable.NumObjects(wtxn))
		err := p.Services.DeleteService(wtxn, name)
		require.NoError(t, err, "DeleteService")

		_, _, found := p.ServiceTable.Get(wtxn, experimental.ServiceNameIndex.Query(name))
		assert.False(t, found, "Frontend found after delete")

		wtxn.Abort()
	}

	// Deletion by source
	{
		wtxn := p.Services.WriteTxn()
		require.Equal(t, 1, p.ServiceTable.NumObjects(wtxn))
		err := p.Services.DeleteServicesBySource(wtxn, source.Kubernetes)
		require.NoError(t, err, "DeleteServicesBySource")

		_, _, found := p.ServiceTable.Get(wtxn, experimental.ServiceNameIndex.Query(name))
		assert.False(t, found, "Service found after delete")

		wtxn.Abort()
	}
}

func TestServices_Backend_UpsertDelete(t *testing.T) {
	p := servicesFixture(t)

	// Add a dump of the state if the test fails. Note that we abort
	// the delete write transactions so they're not visible via this.
	t.Cleanup(func() {
		if t.Failed() {
			p.Services.DebugDump(p.DB.ReadTxn(), os.Stdout)
		}
	})

	name1 := loadbalancer.ServiceName{Namespace: "test", Name: "test1"}
	name2 := loadbalancer.ServiceName{Namespace: "test", Name: "test2"}

	nextAddr := 0
	mkAddr := func(port uint16) loadbalancer.L3n4Addr {
		nextAddr++
		addrCluster := intToAddr(nextAddr)
		return *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster, port, loadbalancer.ScopeExternal)
	}
	frontend := mkAddr(3000)

	// Add a service with [name1] for backends to refer to.
	// [name2] is left non-existing.
	{
		wtxn := p.Services.WriteTxn()

		created, err := p.Services.UpsertService(
			wtxn,
			&experimental.Service{
				Name:   name1,
				Source: source.Kubernetes,
				Props:  experimental.EmptyProps,
				Frontends: experimental.NewFrontendsMap(
					experimental.Frontend{
						Address: frontend,
						Type:    loadbalancer.SVCTypeClusterIP,
					}),
			})

		require.True(t, created, "Service created")
		require.NoError(t, err, "UpsertService")
		wtxn.Commit()
	}

	svc, _, found := p.ServiceTable.Get(p.DB.ReadTxn(), experimental.ServiceFrontendsIndex.Query(frontend))
	require.True(t, found, "Lookup service")

	// UpsertBackends
	beAddr1, beAddr2, beAddr3 := mkAddr(4000), mkAddr(5000), mkAddr(6000)
	{
		wtxn := p.Services.WriteTxn()

		// Add two backends for [name1].
		p.Services.UpsertBackends(
			wtxn,
			name1,
			source.Kubernetes,
			&loadbalancer.Backend{
				L3n4Addr: beAddr1,
				State:    loadbalancer.BackendStateActive,
			},
			&loadbalancer.Backend{
				L3n4Addr: beAddr2,
				State:    loadbalancer.BackendStateActive,
			},
		)

		// Add a backend for the non-existing [name2].
		p.Services.UpsertBackends(
			wtxn,
			name2,
			source.Kubernetes,
			&loadbalancer.Backend{
				L3n4Addr: beAddr3,
				State:    loadbalancer.BackendStateActive,
			},
		)

		wtxn.Commit()
	}

	// Lookup
	{
		txn := p.DB.ReadTxn()

		// By address
		for _, addr := range []loadbalancer.L3n4Addr{beAddr1, beAddr2, beAddr3} {
			be, _, found := p.BackendTable.Get(txn, experimental.BackendAddrIndex.Query(addr))
			if assert.True(t, found, "Backend not found with address %s", addr) {
				assert.True(t, be.L3n4Addr.DeepEqual(&addr), "Backend address %s does not match %s", be.L3n4Addr, addr)
			}
		}

		// By service
		bes := statedb.Collect(p.BackendTable.List(txn, experimental.BackendServiceIndex.Query(name1)))
		require.Len(t, bes, 2)
		require.True(t, bes[0].L3n4Addr.DeepEqual(&beAddr1))
		require.True(t, bes[1].L3n4Addr.DeepEqual(&beAddr2))

		// Backends for [name2] can be found even though the service doesn't exist (yet).
		bes = statedb.Collect(p.BackendTable.List(txn, experimental.BackendServiceIndex.Query(name2)))
		require.Len(t, bes, 1)
		require.True(t, bes[0].L3n4Addr.DeepEqual(&beAddr3))
	}

	// GetBackendsForService
	{
		txn := p.DB.ReadTxn()

		bes := statedb.Collect(experimental.GetBackendsForService(txn, p.BackendTable, svc))
		require.Len(t, bes, 2)
		require.True(t, bes[0].L3n4Addr.DeepEqual(&beAddr1))
		require.True(t, bes[1].L3n4Addr.DeepEqual(&beAddr2))
	}

	// UpdateBackendState
	{
		wtxn := p.Services.WriteTxn()
		err := p.Services.UpdateBackendState(wtxn, beAddr2, loadbalancer.BackendStateMaintenance)
		require.NoError(t, err, "UpdateBackendState")

		// Service has been bumped and is pending.
		svc, _, found := p.ServiceTable.Get(wtxn, experimental.ServiceFrontendsIndex.Query(frontend))
		require.True(t, found, "Lookup service")
		require.Equal(t, reconciler.StatusKindPending, svc.Status.Kind, "Expected to be pending")

		wtxn.Abort()
	}

	// ReleaseBackend
	{
		wtxn := p.Services.WriteTxn()

		// Release the [name1] reference to [beAddr1].
		require.Equal(t, 3, p.BackendTable.NumObjects(wtxn))
		err := p.Services.ReleaseBackend(wtxn, name1, beAddr1)
		require.NoError(t, err, "ReleaseBackend")

		// [beAddr2] remains for [name1].
		bes := statedb.Collect(experimental.GetBackendsForService(wtxn, p.BackendTable, svc))
		require.Len(t, bes, 1)
		require.True(t, bes[0].L3n4Addr.DeepEqual(&beAddr2))

		wtxn.Abort()
	}

	// DeleteBackendsBySource
	{
		wtxn := p.Services.WriteTxn()

		require.Equal(t, 3, p.BackendTable.NumObjects(wtxn))
		err := p.Services.DeleteBackendsBySource(wtxn, source.Kubernetes)
		require.NoError(t, err, "DeleteBackendsBySource")
		iter, _ := p.BackendTable.All(wtxn)
		require.Len(t, statedb.Collect(iter), 0)

		// No backends remain for the service.
		bes := statedb.Collect(experimental.GetBackendsForService(wtxn, p.BackendTable, svc))
		require.Len(t, bes, 0)

		wtxn.Abort()
	}
}

// TestFrontendJSONRoundtrip validates that Frontend can be marshalled and unmarshalled
// into JSON without information loss. The JSON representation is used for example with the
// StateDB client in cilium-dbg.
func TestFrontendJSONRoundtrip(t *testing.T) {
	return // FIXME

	name := loadbalancer.ServiceName{Namespace: "test", Name: "test1"}
	addr := *loadbalancer.NewL3n4Addr(loadbalancer.TCP, intToAddr(1), 1234, loadbalancer.ScopeExternal)

	svc := &experimental.Service{
		Name:                      name,
		Source:                    source.Kubernetes,
		Props:                     experimental.EmptyProps,
		Labels:                    map[string]string{},
		Selector:                  map[string]string{},
		Annotations:               map[string]string{},
		ExtTrafficPolicy:          loadbalancer.SVCTrafficPolicyCluster,
		IntTrafficPolicy:          loadbalancer.SVCTrafficPolicyLocal,
		NatPolicy:                 loadbalancer.SVCNatPolicyNat46,
		SessionAffinity:           true,
		SessionAffinityTimeoutSec: 1234,
		HealthCheckNodePort:       12345,
		LoadBalancerSourceRanges:  []*cidr.CIDR{cidr.MustParseCIDR("1.2.3.0/24")},
		LoopbackHostport:          true,
		IncludeExternal:           false,
		Shared:                    false,
		ServiceAffinity:           "",
		L7ProxyPort:               1234,
		L7FrontendPorts:           container.NewImmSet[uint16](1234),
		Frontends:                 experimental.NewFrontendsMap(experimental.Frontend{Address: addr, Type: loadbalancer.SVCTypeClusterIP, ID: 1234}),
		BackendsRevision:          0,
		Status:                    reconciler.StatusPending(),
	}

	svcBytes, err := json.Marshal(svc)
	require.NoError(t, err, "Marshal")

	var svc2 experimental.Service
	err = json.Unmarshal(svcBytes, &svc2)
	require.NoError(t, err, "Unmarshal")

	svcBytes2, err := json.Marshal(svc)
	require.NoError(t, err, "Marshal")

	// Since equality is tricky thing to do correctly, especially with netip.Addr for example
	// ending up using the 4in6 presentation, we instead compare just the JSON bytes and the TableRow
	// output.
	require.Equal(t, svcBytes, svcBytes2, "marshalled output mismatch")
	require.Equal(t, svc.TableRow(), svc2.TableRow(), "TableRow mismatch")

	// Validate the table row output makes sense
	expected := "test/test1 123 1.0.0.1:1234/TCP ClusterIP k8s Pending " // (???s ago)
	actual := strings.Join(svc2.TableRow(), " ")
	assert.True(t, strings.HasPrefix(actual, expected), "expected prefix %q, got %q", expected, actual)
}

func TestBackendJSONRoundtrip(t *testing.T) {
	name1 := loadbalancer.ServiceName{Namespace: "test", Name: "test1"}
	name2 := loadbalancer.ServiceName{Namespace: "test", Name: "test2"}
	addr := *loadbalancer.NewL3n4Addr(loadbalancer.TCP, intToAddr(1), 1234, loadbalancer.ScopeExternal)

	be := &experimental.Backend{
		Backend: loadbalancer.Backend{
			FEPortName: "some-port",
			ID:         123,
			Weight:     123,
			NodeName:   "some-node",
			ZoneID:     123,
			L3n4Addr:   addr,
			State:      loadbalancer.BackendStateMaintenance,
			Preferred:  true,
		},
		Cluster:      "test",
		Source:       source.Kubernetes,
		ReferencedBy: experimental.NewServiceNameSet(name1, name2),
	}

	beBytes, err := json.Marshal(be)
	require.NoError(t, err, "Marshal")

	var be2 experimental.Backend
	err = json.Unmarshal(beBytes, &be2)
	require.NoError(t, err, "Unmarshal")

	beBytes2, err := json.Marshal(be)
	require.NoError(t, err, "Marshal")

	// Since equality is tricky thing to do correctly, especially with netip.Addr for example
	// ending up using the 4in6 presentation, we instead compare just the JSON bytes and the TableRow
	// output.
	require.Equal(t, beBytes, beBytes2, "marshalled output mismatch")
	require.Equal(t, be.TableRow(), be2.TableRow(), "TableRow mismatch")

	// Validate the table row output makes sense
	expected := "1.0.0.1:1234/TCP maintenance k8s test/test1, test/test2"
	actual := strings.Join(be2.TableRow(), " ")
	assert.True(t, strings.HasPrefix(actual, expected), "expected prefix %q, got %q", expected, actual)
}
