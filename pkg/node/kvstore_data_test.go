// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"encoding/json"
	"iter"
	"maps"
	"net"
	"net/netip"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/cilium/cilium/pkg/ip"
	"github.com/cilium/cilium/pkg/node/addressing"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
)

type addressOrderData struct {
	Data
	addresses []Address
}

func (d addressOrderData) Addresses() iter.Seq[Address] {
	return slices.Values(d.addresses)
}

func TestKVStoreDataAddresses(t *testing.T) {
	data := nodeTypes.NewKVStoreData(&nodeTypes.KVStoreNode{
		IPAddresses: []nodeTypes.Address{
			{Type: addressing.NodeInternalIP, IP: net.ParseIP("10.0.0.1")},
			{Type: addressing.NodeExternalIP, IP: net.ParseIP("2001:db8::1")},
		},
		IPv4AllocCIDR: nodeTypes.PrefixFrom(netip.MustParsePrefix("10.1.0.0/16")),
		IPv6HealthIP:  ip.AddrFrom(netip.MustParseAddr("2001:db8::2")),
		IPv4IngressIP: ip.AddrFrom(netip.MustParseAddr("10.0.0.2")),
	})

	got := slices.Collect(data.Addresses())
	require.ElementsMatch(t, []Address{
		{Kind: AddressKindNode, NodeAddressType: addressing.NodeInternalIP, Prefix: netip.MustParsePrefix("10.0.0.1/32")},
		{Kind: AddressKindNode, NodeAddressType: addressing.NodeExternalIP, Prefix: netip.MustParsePrefix("2001:db8::1/128")},
		{Kind: AddressKindAllocation, Prefix: netip.MustParsePrefix("10.1.0.0/16"), Primary: true},
		{Kind: AddressKindHealth, Prefix: netip.MustParsePrefix("2001:db8::2/128")},
		{Kind: AddressKindIngress, Prefix: netip.MustParsePrefix("10.0.0.2/32")},
	}, got)
}

func TestKVStoreDataMetadata(t *testing.T) {
	data := nodeTypes.NewKVStoreData(&nodeTypes.KVStoreNode{
		Labels:      map[string]string{"label": "value"},
		Annotations: map[string]string{"annotation": "value"},
	})

	require.Equal(t, map[string]string{"label": "value"}, maps.Collect(data.Labels()))
	require.Equal(t, map[string]string{"annotation": "value"}, maps.Collect(data.Annotations()))
	require.Equal(t, "value", mustGet(data.Label("label")))
	require.Equal(t, "value", mustGet(data.Annotation("annotation")))
}

func mustGet(value string, found bool) string {
	if !found {
		panic("value not found")
	}
	return value
}

func TestNodeMarshalUsesKVStoreShape(t *testing.T) {
	wire := &nodeTypes.KVStoreNode{
		Name: "node-1", Labels: map[string]string{"k": "v"},
		Annotations: map[string]string{},
	}
	n := FromKVStoreNode(wire)

	wantJSON, err := json.Marshal(wire)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(n)
	require.NoError(t, err)
	require.JSONEq(t, string(wantJSON), string(gotJSON))

	gotYAML, err := yaml.Marshal(n)
	require.NoError(t, err)
	require.Contains(t, string(gotYAML), "name: node-1")
}

func TestToKVStoreNodeHonorsPrimaryAllocationCIDR(t *testing.T) {
	base := nodeTypes.NewKVStoreData(&nodeTypes.KVStoreNode{Name: "node-1"})
	n := New(addressOrderData{
		Data: base,
		addresses: []Address{
			{
				Kind:   AddressKindAllocation,
				Prefix: netip.MustParsePrefix("10.2.0.0/16"),
			},
			{
				Kind:    AddressKindAllocation,
				Prefix:  netip.MustParsePrefix("10.1.0.0/16"),
				Primary: true,
			},
		},
	})

	wire := n.ToKVStoreNode()
	require.Equal(t, netip.MustParsePrefix("10.1.0.0/16"),
		wire.IPv4AllocCIDR.Prefix.Prefix)
	require.Equal(t, []nodeTypes.Prefix{
		nodeTypes.PrefixFrom(netip.MustParsePrefix("10.2.0.0/16")),
	}, wire.IPv4SecondaryAllocCIDRs)
}
