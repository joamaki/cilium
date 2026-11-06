// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package api

import (
	"fmt"
	"net/netip"

	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/statedb"
)

type hostPortQuota struct {
	db        *statedb.DB
	addresses statedb.Table[tables.NodeAddress]
}

func newHostPortQuota(db *statedb.DB, addresses statedb.Table[tables.NodeAddress]) *hostPortQuota {
	return &hostPortQuota{db: db, addresses: addresses}
}

func (q *hostPortQuota) count(mappings []*models.PortMapping, ipv4, ipv6 bool) (uint64, error) {
	type frontend struct {
		ip       netip.Addr
		port     int64
		protocol string
	}
	entries := map[frontend]struct{}{}
	txn := q.db.ReadTxn()
	for _, p := range mappings {
		if p == nil || p.HostPort <= 0 {
			continue
		}
		proto := "tcp"
		if p.Protocol != nil {
			proto = *p.Protocol
		}
		var ip netip.Addr
		if p.HostIP != "" {
			var err error
			ip, err = netip.ParseAddr(p.HostIP)
			if err != nil {
				return 0, fmt.Errorf("invalid HostPort host IP %q: %w", p.HostIP, err)
			}
			ip = ip.Unmap()
		}
		if ip.IsValid() && !ip.IsUnspecified() {
			if (ip.Is4() && ipv4) || (ip.Is6() && ipv6) {
				entries[frontend{ip, p.HostPort, proto}] = struct{}{}
			}
			continue
		}
		for address := range q.addresses.All(txn) {
			if !address.NodePort || (address.Addr.Is4() && !ipv4) || (address.Addr.Is6() && !ipv6) {
				continue
			}
			if ip.IsValid() && address.Addr.Is4() != ip.Is4() {
				continue
			}
			entries[frontend{address.Addr, p.HostPort, proto}] = struct{}{}
		}
	}
	return uint64(len(entries)), nil
}
