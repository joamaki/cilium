// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package experimental_test

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/loadbalancer/experimental"
	"github.com/cilium/cilium/pkg/source"
)

func BenchmarkInsertService(b *testing.B) {
	p := servicesFixture(b)

	b.ResetTimer()

	numObjects := 1000

	// Add 'numObjects' existing objects to the table.
	wtxn := p.Services.WriteTxn()
	for i := 0; i < numObjects; i++ {
		name := loadbalancer.ServiceName{Namespace: "test-existing", Name: fmt.Sprintf("svc-%d", i)}
		var addr1 [4]byte
		binary.BigEndian.PutUint32(addr1[:], 0x02000000+uint32(i))
		addrCluster, _ := types.AddrClusterFromIP(addr1[:])
		p.Services.UpsertService(
			wtxn,
			&experimental.Service{
				Name: name,
				Frontends: experimental.NewFrontendsMap(
					experimental.Frontend{
						Address:  *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster, 12345, loadbalancer.ScopeExternal),
						Type:     loadbalancer.SVCTypeClusterIP,
						PortName: "",
						Props:    experimental.EmptyProps,
						ID:       0,
					},
				),
			},
		)
	}
	wtxn.Commit()

	// Benchmark the speed at which a new service is upserted. 'numObjects' are inserted in one
	// WriteTxn to amortize the cost of WriteTxn&Commit.
	for n := 0; n < b.N; n++ {
		wtxn := p.Services.WriteTxn()
		for i := 0; i < numObjects; i++ {
			name := loadbalancer.ServiceName{Namespace: "test-new", Name: fmt.Sprintf("svc-%d", i)}
			var addr1 [4]byte
			binary.BigEndian.PutUint32(addr1[:], 0x01000000+uint32(i))
			addrCluster, _ := types.AddrClusterFromIP(addr1[:])
			p.Services.UpsertService(
				wtxn,
				&experimental.Service{
					Name: name,
					Frontends: experimental.NewFrontendsMap(
						experimental.Frontend{
							Address:  *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster, 12345, loadbalancer.ScopeExternal),
							Type:     loadbalancer.SVCTypeClusterIP,
							PortName: "",
							Props:    experimental.EmptyProps,
							ID:       0,
						},
					),
				},
			)
		}
		wtxn.Abort()
	}

	b.StopTimer()
	b.ReportMetric(float64(b.N*numObjects)/b.Elapsed().Seconds(), "objects/sec")
}

func BenchmarkInsertBackend(b *testing.B) {
	p := servicesFixture(b)

	b.ResetTimer()

	addrCluster1 := types.MustParseAddrCluster("1.0.0.1")
	addrCluster2 := types.MustParseAddrCluster("2.0.0.2")

	name := loadbalancer.ServiceName{Namespace: "test", Name: "svc"}
	wtxn := p.Services.WriteTxn()

	p.Services.UpsertService(
		wtxn,
		&experimental.Service{
			Name:   name,
			Source: source.Kubernetes,
			Frontends: experimental.NewFrontendsMap(
				experimental.Frontend{
					Address:  *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster1, 12345, loadbalancer.ScopeExternal),
					Type:     loadbalancer.SVCTypeClusterIP,
					PortName: "",
					Props:    experimental.EmptyProps,
					ID:       0,
				},
			),
		},
	)
	wtxn.Commit()

	numObjects := 1000

	// Add 'numObjects' existing objects to the table.
	wtxn = p.Services.WriteTxn()
	for i := 0; i < numObjects; i++ {
		beAddr := *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster1, uint16(i), loadbalancer.ScopeExternal)
		p.Services.UpsertBackends(
			wtxn,
			name,
			source.Kubernetes,
			&loadbalancer.Backend{
				L3n4Addr: beAddr,
				State:    loadbalancer.BackendStateActive,
			},
		)
	}
	wtxn.Abort()

	// Benchmark the speed at which a new backend is upserted. 'numObjects' are inserted in one
	// WriteTxn to amortize the cost of WriteTxn&Commit.
	for n := 0; n < b.N; n++ {
		wtxn = p.Services.WriteTxn()
		for i := 0; i < numObjects; i++ {
			beAddr := *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster2, uint16(i), loadbalancer.ScopeExternal)
			p.Services.UpsertBackends(
				wtxn,
				name,
				source.Kubernetes,
				&loadbalancer.Backend{
					L3n4Addr: beAddr,
					State:    loadbalancer.BackendStateActive,
				},
			)
		}
		// Don't commit the changes so we actually test the cost of Insert() of new object.
		wtxn.Abort()
	}

	b.StopTimer()
	b.ReportMetric(float64(b.N*numObjects)/b.Elapsed().Seconds(), "objects/sec")
}
func BenchmarkReplaceBackend(b *testing.B) {
	p := servicesFixture(b)

	b.ResetTimer()

	addrCluster1 := types.MustParseAddrCluster("1.0.0.1")
	addrCluster2 := types.MustParseAddrCluster("2.0.0.2")

	name := loadbalancer.ServiceName{Namespace: "test", Name: "svc"}
	wtxn := p.Services.WriteTxn()

	p.Services.UpsertService(
		wtxn,
		&experimental.Service{
			Name:   name,
			Source: source.Kubernetes,
			Frontends: experimental.NewFrontendsMap(
				experimental.Frontend{
					Address:  *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster1, 12345, loadbalancer.ScopeExternal),
					Type:     loadbalancer.SVCTypeClusterIP,
					PortName: "",
					Props:    experimental.EmptyProps,
					ID:       0,
				},
			),
		},
	)

	beAddr := *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster2, uint16(1234), loadbalancer.ScopeExternal)
	p.Services.UpsertBackends(
		wtxn,
		name,
		source.Kubernetes,
		&loadbalancer.Backend{
			L3n4Addr: beAddr,
			State:    loadbalancer.BackendStateActive,
		},
	)
	wtxn.Commit()

	wtxn = p.Services.WriteTxn()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Services.UpsertBackends(
			wtxn,
			name,
			source.Kubernetes,
			&loadbalancer.Backend{
				L3n4Addr: beAddr,
				State:    loadbalancer.BackendStateActive,
			},
		)
	}
	wtxn.Abort()

	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "objects/sec")
}

func BenchmarkReplaceService(b *testing.B) {
	p := servicesFixture(b)

	b.ResetTimer()

	addrCluster := types.MustParseAddrCluster("1.0.0.1")
	l3n4Addr := *loadbalancer.NewL3n4Addr(loadbalancer.TCP, addrCluster, 12345, loadbalancer.ScopeExternal)

	name := loadbalancer.ServiceName{Namespace: "test", Name: "svc"}
	wtxn := p.Services.WriteTxn()

	p.Services.UpsertService(
		wtxn,
		&experimental.Service{
			Name:   name,
			Source: source.Kubernetes,
			Frontends: experimental.NewFrontendsMap(
				experimental.Frontend{
					Address:  l3n4Addr,
					Type:     loadbalancer.SVCTypeClusterIP,
					PortName: "",
					Props:    experimental.EmptyProps,
					ID:       0,
				},
			),
		},
	)

	wtxn.Commit()

	b.ResetTimer()

	// Replace the service b.N times
	wtxn = p.Services.WriteTxn()
	for i := 0; i < b.N; i++ {
		p.Services.UpsertService(
			wtxn,
			&experimental.Service{
				Name:   name,
				Source: source.Kubernetes,
				Frontends: experimental.NewFrontendsMap(
					experimental.Frontend{
						Address:  l3n4Addr,
						Type:     loadbalancer.SVCTypeClusterIP,
						PortName: "",
						Props:    experimental.EmptyProps,
						ID:       0,
					},
				),
			},
		)
	}
	wtxn.Abort()

	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "objects/sec")
}
