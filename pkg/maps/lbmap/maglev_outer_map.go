// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package lbmap

import (
	"fmt"

	"github.com/cilium/cilium/pkg/bpf"
	"github.com/cilium/cilium/pkg/byteorder"
	"github.com/cilium/cilium/pkg/ebpf"
)

// MaglevOuterMap represents a Maglev outer map.
type MaglevOuterMap struct {
	*ebpf.Map
}

// UpdateService sets the given inner map to be the Maglev lookup table for
// the service with the given id.
func (m *MaglevOuterMap) UpdateService(id uint16, inner *MaglevInnerMap) error {
	key := MaglevOuterKey{RevNatID: id}.toNetwork()
	val := MaglevOuterVal{FD: uint32(inner.FD())}
	return m.Map.Update(key, val, 0)
}

// MaglevOuterKey is the key of a maglev outer map.
type MaglevOuterKey struct {
	RevNatID uint16
}

// New and String implement bpf.MapKey
func (k *MaglevOuterKey) New() bpf.MapKey { return &MaglevOuterKey{} }
func (k *MaglevOuterKey) String() string  { return fmt.Sprintf("%d", k.RevNatID) }

var _ bpf.MapKey = &MaglevOuterKey{}

// toNetwork converts a maglev outer map's key to network byte order.
// The key is in network byte order in the eBPF maps.
func (k MaglevOuterKey) toNetwork() MaglevOuterKey {
	return MaglevOuterKey{
		RevNatID: byteorder.HostToNetwork16(k.RevNatID),
	}
}

// MaglevOuterVal is the value of a maglev outer map.
type MaglevOuterVal struct {
	FD uint32
}
