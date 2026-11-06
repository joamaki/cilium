// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quota

import (
	"errors"
	"fmt"

	"github.com/cilium/statedb"
)

// ErrQuotaExceeded is returned when an acquisition would exceed the quota's
// hard limit.
var ErrQuotaExceeded = errors.New("quota exceeded")

// Accounting manages named reservations in caller-owned StateDB transactions.
// Reserve sets a reservation's total amount; repeating it is idempotent. Only
// increases enforce the hard limit. Restore reconstructs existing allocations
// even above the limit. Release of an unknown reservation is a no-op.
type Accounting interface {
	// Table returns the quota table for inclusion in caller-owned transactions.
	Table() statedb.Table[ResourceQuota]
	// Reserve returns the previous amount, including on rejection. Errors leave
	// the reservation unchanged. A missing reservation has amount zero.
	Reserve(txn statedb.WriteTxn, namespace string, resource ResourceName, id ReservationID, amount uint64) (previous uint64, err error)
	Restore(txn statedb.WriteTxn, namespace string, resource ResourceName, id ReservationID, amount uint64) error
	Release(txn statedb.WriteTxn, namespace string, resource ResourceName, id ReservationID) error
}

type accounting struct {
	table statedb.RWTable[ResourceQuota]
}

// NewAccounting creates a transactional quota accounting implementation.
func NewAccounting(table statedb.RWTable[ResourceQuota]) Accounting {
	return &accounting{table: table}
}

func (a *accounting) Table() statedb.Table[ResourceQuota] { return a.table.ToTable() }

// Reserve sets the total reservation, charging only its increase. It returns
// the previous amount, or zero for a new reservation. Errors leave it unchanged.
func (a *accounting) Reserve(txn statedb.WriteTxn, namespace string, resource ResourceName, id ReservationID, amount uint64) (previous uint64, err error) {
	return a.reserve(txn, namespace, resource, id, amount, false)
}

// Restore reconstructs an existing reservation without enforcing a lowered limit.
func (a *accounting) Restore(txn statedb.WriteTxn, namespace string, resource ResourceName, id ReservationID, amount uint64) error {
	_, err := a.reserve(txn, namespace, resource, id, amount, true)
	return err
}

func (a *accounting) reserve(txn statedb.WriteTxn, namespace string, resource ResourceName, id ReservationID, amount uint64, restore bool) (uint64, error) {
	row, _, found := a.table.Get(txn, ResourceQuotaByName(namespace, resource))
	if !found {
		row = ResourceQuota{Namespace: namespace, Name: resource, Hard: Unlimited}
	}
	previous, _ := row.Reservations.Get(id)
	if amount == 0 {
		return previous, a.Release(txn, namespace, resource, id)
	}
	if len(row.Inputs) == 0 {
		row.Hard = Unlimited
		if defaults, _, exists := a.table.Get(txn, ResourceQuotaByName("", resource)); exists {
			row.Hard = defaults.Hard
		}
	}
	if amount > previous {
		increase := amount - previous
		if increase > Unlimited-row.Used {
			return previous, fmt.Errorf("quota usage overflow: namespace=%q resource=%q", namespace, resource)
		}
		if !restore && (row.Used > row.Hard || increase > row.Hard-row.Used) {
			return previous, fmt.Errorf("%w: namespace=%q resource=%q reservation=%q requested=%d used=%d hard=%d", ErrQuotaExceeded, namespace, resource, id, amount, row.Used, row.Hard)
		}
		row.Used += increase
	} else {
		row.Used -= previous - amount
	}
	row.Reservations = row.Reservations.Set(id, amount)
	_, _, err := a.table.Insert(txn, row)
	return previous, err
}

func (a *accounting) Release(txn statedb.WriteTxn, namespace string, resource ResourceName, id ReservationID) error {
	row, _, found := a.table.Get(txn, ResourceQuotaByName(namespace, resource))
	if !found {
		return nil
	}
	amount, found := row.Reservations.Get(id)
	if !found {
		return nil
	}
	row.Used -= amount
	row.Reservations = row.Reservations.Delete(id)
	if row.Used == 0 && len(row.Inputs) == 0 {
		_, _, err := a.table.Delete(txn, row)
		return err
	}
	_, _, err := a.table.Insert(txn, row)
	return err
}
