// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quotasources

import (
	"testing"

	"github.com/cilium/cilium/pkg/quota"
	"github.com/cilium/statedb"
	"github.com/stretchr/testify/require"
)

func TestParseConfigQuota(t *testing.T) {
	for _, tc := range []struct {
		key, value, namespace string
		hard                  uint64
		invalid               bool
	}{
		{key: "*:cilium.io/service-ports", value: "10k", hard: 10000},
		{key: "team-a:cilium.io/service-ports", value: "0", namespace: "team-a"},
		{key: " team-a : cilium.io/service-ports ", value: " 2Ki ", namespace: "team-a", hard: 2048},
		{key: "*:cilium.io/service-ports", value: "18446744073709551615", hard: ^uint64(0)},
		{key: "*:cilium.io/service-ports", value: "18446744073709551616", invalid: true},
		{key: "*:cilium.io/service-ports", value: "-1", invalid: true},
		{key: "*:cilium.io/service-ports", value: "1.5", invalid: true},
		{key: "*:cilium.io/service-ports", value: "1.9999999999", invalid: true},
		{key: "*:cilium.io/service-ports", value: "8Ei", hard: 1 << 63},
		{key: "*:cilium.io/service-ports", value: "16Ei", invalid: true},
		{key: "*:cilium.io/service-ports", value: "1e4", hard: 10000},
		{key: "*:cilium.io/service-ports", value: "1m", invalid: true},
		{key: "*:cilium.io/service-ports", value: "garbage", invalid: true},
		{key: "cilium.io/service-ports", value: "1", invalid: true},
		{key: ":cilium.io/service-ports", value: "1", invalid: true},
		{key: "team.a:cilium.io/service-ports", value: "1", invalid: true},
		{key: "team-a:", value: "1", invalid: true},
		{key: "team-a:cilium.io/service:ports", value: "1", invalid: true},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			namespace, name, hard, err := parseConfigQuota(tc.key, tc.value)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.namespace, namespace)
			require.Equal(t, "cilium.io/service-ports", string(name))
			require.Equal(t, tc.hard, hard)
		})
	}
}

func TestApplyConfigRejectsInvalidInputAtomically(t *testing.T) {
	for _, limits := range []map[string]string{
		{"team-a:cilium.io/service-ports": "1", " team-a : cilium.io/service-ports ": "2"},
		{"*:cilium.io/service-ports": "1", "team-a:invalid:name": "2"},
	} {
		db := statedb.New()
		table, err := quota.NewTable(db)
		require.NoError(t, err)
		require.Error(t, ApplyConfig(db, table, Config{Limits: limits}))
		require.Zero(t, table.NumObjects(db.ReadTxn()))
	}
}
