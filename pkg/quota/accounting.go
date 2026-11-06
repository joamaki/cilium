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

// Accounting transactionally acquires and releases quota usage.
type Accounting interface {
	Acquire(txn statedb.WriteTxn, namespace string, resource ResourceName, amount uint64) error
	Release(txn statedb.WriteTxn, namespace string, resource ResourceName, amount uint64) error
}

type accounting struct {
	table statedb.RWTable[ResourceQuota]
}

// NewAccounting creates a transactional quota accounting implementation.
func NewAccounting(table statedb.RWTable[ResourceQuota]) Accounting {
	return &accounting{table: table}
}

func (a *accounting) Acquire(txn statedb.WriteTxn, namespace string, resource ResourceName, amount uint64) error {
	if amount == 0 {
		return nil
	}

	quota, _, found := a.table.Get(txn, ResourceQuotaByName(namespace, resource))
	if !found {
		// Track usage even when no namespace quota is configured. A default
		// quota from the configuration source applies when no namespace input
		// exists.
		quota = ResourceQuota{Namespace: namespace, Name: resource, Hard: Unlimited}
		if defaultQuota, _, exists := a.table.Get(txn, ResourceQuotaByName("", resource)); exists {
			quota.Hard = defaultQuota.Hard
		}
	}
	if quota.Used > quota.Hard || amount > quota.Hard-quota.Used {
		return fmt.Errorf("%w: namespace=%q resource=%q requested=%d used=%d hard=%d",
			ErrQuotaExceeded, namespace, resource, amount, quota.Used, quota.Hard)
	}
	quota.Used += amount
	_, _, err := a.table.Insert(txn, quota)
	return err
}

func (a *accounting) Release(txn statedb.WriteTxn, namespace string, resource ResourceName, amount uint64) error {
	if amount == 0 {
		return nil
	}

	quota, _, found := a.table.Get(txn, ResourceQuotaByName(namespace, resource))
	if !found {
		return nil
	}
	if amount > quota.Used {
		return fmt.Errorf("quota release exceeds current usage: namespace=%q resource=%q requested=%d used=%d",
			namespace, resource, amount, quota.Used)
	}
	quota.Used -= amount
	if len(quota.Inputs) == 0 && quota.Used == 0 {
		_, _, err := a.table.Delete(txn, quota)
		return err
	}
	_, _, err := a.table.Insert(txn, quota)
	return err
}
