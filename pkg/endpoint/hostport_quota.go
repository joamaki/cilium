// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package endpoint

import (
	"fmt"

	"github.com/cilium/cilium/pkg/quota"
	"github.com/google/uuid"
)

// hostPortReservation persists the accepted charge rather than recomputing it
// from Pod or node state during restoration.
type hostPortReservation struct {
	Namespace string
	ID        quota.ReservationID
	Amount    uint64
}

// ReserveHostPorts records the ADD-time quota charge before endpoint creation.
func (e *Endpoint) ReserveHostPorts(amount uint64) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	if amount == 0 {
		return nil
	}
	r := e.hostPortReservation
	if r == nil {
		r = &hostPortReservation{Namespace: e.K8sNamespace, ID: quota.ReservationID(fmt.Sprintf("hostport/%s/%s", e.GetCNIAttachmentID(), uuid.NewString()))}
	}
	txn := e.quotaDB.WriteTxn(e.quotaAccounting.Table())
	defer txn.Abort()
	if _, err := e.quotaAccounting.Reserve(txn, r.Namespace, quota.HostPorts, r.ID, amount); err != nil {
		return fmt.Errorf("HostPort setup: %w", err)
	}
	txn.Commit()
	e.hostPortReservation = &hostPortReservation{Namespace: r.Namespace, ID: r.ID, Amount: amount}
	return nil
}

// ReleaseHostPorts rolls back a failed creation. Endpoint deletion calls the
// locked implementation directly. Repeated release is harmless.
func (e *Endpoint) ReleaseHostPorts() error {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return e.releaseHostPortsLocked()
}

func (e *Endpoint) releaseHostPortsLocked() error {
	r := e.hostPortReservation
	if r == nil {
		return nil
	}
	txn := e.quotaDB.WriteTxn(e.quotaAccounting.Table())
	defer txn.Abort()
	if err := e.quotaAccounting.Release(txn, r.Namespace, quota.HostPorts, r.ID); err != nil {
		return err
	}
	txn.Commit()
	e.hostPortReservation = nil
	return nil
}

// RestoreHostPorts reconstructs an accepted reservation before exposing a
// restored endpoint, including when its limit has subsequently been lowered.
func (e *Endpoint) RestoreHostPorts() error {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	r := e.hostPortReservation
	if r == nil {
		// Older snapshots have no recorded charge. They cannot be backfilled
		// without another source of the original CNI arguments.
		return nil
	}
	txn := e.quotaDB.WriteTxn(e.quotaAccounting.Table())
	defer txn.Abort()
	if err := e.quotaAccounting.Restore(txn, r.Namespace, quota.HostPorts, r.ID, r.Amount); err != nil {
		return err
	}
	txn.Commit()
	return nil
}
