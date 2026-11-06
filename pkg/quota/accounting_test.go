// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quota

import (
	"testing"

	"github.com/cilium/statedb"
	"github.com/stretchr/testify/require"
)

func TestReservePrevious(t *testing.T) {
	db := statedb.New()
	table, err := NewTable(db)
	require.NoError(t, err)
	accounting := NewAccounting(table)
	resource := ResourceName("cilium.io/test")
	row := ResourceQuota{Namespace: "test", Name: resource}
	row.AddInput(ResourceQuotaInput{Hard: 2})
	txn := db.WriteTxn(table)
	_, _, err = table.Insert(txn, row)
	require.NoError(t, err)
	txn.Commit()
	for _, tc := range []struct {
		name                   string
		amount, previous, used uint64
		exceeded               bool
	}{
		{name: "new", amount: 1, used: 1},
		{name: "repeat", amount: 1, previous: 1, used: 1},
		{name: "grow", amount: 2, previous: 1, used: 2},
		{name: "rejected", amount: 3, previous: 2, used: 2, exceeded: true},
		{name: "shrink", amount: 1, previous: 2, used: 1},
		{name: "zero releases", previous: 1},
		{name: "missing zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			txn := db.WriteTxn(table)
			defer txn.Abort()
			previous, err := accounting.Reserve(txn, "test", resource, "entity", tc.amount)
			require.Equal(t, tc.previous, previous)
			if tc.exceeded {
				require.ErrorIs(t, err, ErrQuotaExceeded)
			} else {
				require.NoError(t, err)
			}
			// Commit even a rejected attempt to verify that it left state untouched.
			txn.Commit()
			row, _, found := table.Get(db.ReadTxn(), ResourceQuotaByName("test", resource))
			require.True(t, found)
			require.Equal(t, tc.used, row.Used)
			held, _ := row.Reservations.Get("entity")
			require.Equal(t, tc.used, held)
		})
	}
}
