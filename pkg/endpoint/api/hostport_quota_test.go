// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package api

import (
	"net/netip"
	"testing"

	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/statedb"
	"github.com/stretchr/testify/require"
)

func TestHostPortQuotaCount(t *testing.T) {
	db := statedb.New()
	addresses, err := tables.NewNodeAddressTable(db)
	require.NoError(t, err)
	txn := db.WriteTxn(addresses)
	for _, address := range []tables.NodeAddress{
		{Addr: netip.MustParseAddr("192.0.2.1"), NodePort: true, DeviceName: "eth0"},
		{Addr: netip.MustParseAddr("192.0.2.1"), NodePort: true, DeviceName: "eth1"},
		{Addr: netip.MustParseAddr("2001:db8::1"), NodePort: true, DeviceName: "eth0"},
		{Addr: netip.MustParseAddr("192.0.2.2"), NodePort: false, DeviceName: "eth0"},
	} {
		_, _, err = addresses.Insert(txn, address)
		require.NoError(t, err)
	}
	txn.Commit()
	q := newHostPortQuota(db, addresses.ToTable())
	udp := "udp"
	for _, tc := range []struct {
		name     string
		mappings []*models.PortMapping
		count    uint64
		invalid  bool
	}{
		{name: "none"},
		{name: "duplicate", mappings: []*models.PortMapping{{HostIP: "127.0.0.1", HostPort: 80}, {HostIP: "127.0.0.1", HostPort: 80}}, count: 1},
		{name: "wildcard", mappings: []*models.PortMapping{{HostPort: 80}}, count: 2},
		{name: "ipv4 wildcard", mappings: []*models.PortMapping{{HostIP: "0.0.0.0", HostPort: 80}}, count: 1},
		{name: "ipv6 wildcard", mappings: []*models.PortMapping{{HostIP: "::", HostPort: 80}}, count: 1},
		{name: "protocols", mappings: []*models.PortMapping{{HostPort: 80}, {HostPort: 80, Protocol: &udp}}, count: 4},
		{name: "invalid", mappings: []*models.PortMapping{{HostIP: "invalid", HostPort: 80}}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count, err := q.count(tc.mappings, true, true)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.count, count)
			}
		})
	}
}

func TestHostPortQuotaCountWithoutFamilies(t *testing.T) {
	db := statedb.New()
	addresses, err := tables.NewNodeAddressTable(db)
	require.NoError(t, err)
	q := newHostPortQuota(db, addresses.ToTable())
	count, err := q.count([]*models.PortMapping{{HostIP: "127.0.0.1", HostPort: 80}}, false, true)
	require.NoError(t, err)
	require.Zero(t, count)
}
