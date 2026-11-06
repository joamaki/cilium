// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package endpoint

import (
	"encoding/json"
	"testing"

	"github.com/cilium/cilium/pkg/quota"
	"github.com/cilium/statedb"
	"github.com/stretchr/testify/require"
)

func TestHostPortQuotaLifecycle(t *testing.T) {
	db := statedb.New()
	table, err := quota.NewTable(db)
	require.NoError(t, err)
	txn := db.WriteTxn(table)
	row := quota.ResourceQuota{Namespace: "test", Name: quota.HostPorts}
	row.AddInput(quota.ResourceQuotaInput{Hard: 1})
	_, _, err = table.Insert(txn, row)
	require.NoError(t, err)
	txn.Commit()
	accounting := quota.NewAccounting(table)
	first := &Endpoint{quotaDB: db, quotaAccounting: accounting, K8sNamespace: "test"}
	second := &Endpoint{quotaDB: db, quotaAccounting: accounting, K8sNamespace: "test"}
	require.NoError(t, first.ReserveHostPorts(1))
	require.NoError(t, first.ReserveHostPorts(1))
	require.ErrorIs(t, second.ReserveHostPorts(1), quota.ErrQuotaExceeded)
	// The endpoint ID is assigned after reservation. Deletion uses the persisted ID.
	id := first.hostPortReservation.ID
	first.ID = 123
	data, err := json.Marshal(first.toSerializedEndpoint())
	require.NoError(t, err)
	var saved serializableEndpoint
	require.NoError(t, json.Unmarshal(data, &saved))
	require.Equal(t, id, saved.HostPortReservation.ID)
	require.NoError(t, first.ReleaseHostPorts())
	require.NoError(t, first.ReleaseHostPorts())
	txn = db.WriteTxn(table)
	row, _, _ = table.Get(txn, quota.ResourceQuotaByName("test", quota.HostPorts))
	row.AddInput(quota.ResourceQuotaInput{Hard: 0})
	_, _, err = table.Insert(txn, row)
	require.NoError(t, err)
	txn.Commit()
	restored := &Endpoint{quotaDB: db, quotaAccounting: accounting}
	restored.fromSerializedEndpoint(&saved)
	require.NoError(t, restored.RestoreHostPorts())
	require.NoError(t, restored.RestoreHostPorts())
	row, _, _ = table.Get(db.ReadTxn(), quota.ResourceQuotaByName("test", quota.HostPorts))
	require.Equal(t, uint64(1), row.Used)
	require.ErrorIs(t, second.ReserveHostPorts(1), quota.ErrQuotaExceeded)
	require.NoError(t, restored.ReleaseHostPorts())
	row, _, _ = table.Get(db.ReadTxn(), quota.ResourceQuotaByName("test", quota.HostPorts))
	require.Zero(t, row.Used)
}
