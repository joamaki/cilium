// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package node

import (
	"encoding/json"
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

func TestKVStoreDataAddresses(t *testing.T) {
	data := DataFromKVStoreNode(&nodeTypes.KVStoreNode{
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
	data := DataFromKVStoreNode(&nodeTypes.KVStoreNode{
		Labels:      map[string]string{"label": "value"},
		Annotations: map[string]string{"annotation": "value"},
	})

	require.Equal(t, map[string]string{"label": "value"}, maps.Collect(data.Labels()))
	require.Equal(t, map[string]string{"annotation": "value"}, maps.Collect(data.Annotations()))
	require.Equal(t, "value", mustGet(data.Label("label")))
	require.Equal(t, "value", mustGet(data.Annotation("annotation")))
}

func TestKVStoreDataSnapshotsInput(t *testing.T) {
	wire := &nodeTypes.KVStoreNode{
		IPAddresses: []nodeTypes.Address{{
			Type: addressing.NodeInternalIP,
			IP:   net.ParseIP("10.0.0.1"),
		}},
		Labels: map[string]string{"label": "original"},
	}
	data := DataFromKVStoreNode(wire)

	wire.IPAddresses[0].IP[0] = 0xff
	wire.Labels["label"] = "mutated"

	require.Equal(t, "original", mustGet(data.Label("label")))
	require.Equal(t, netip.MustParseAddr("10.0.0.1"),
		slices.Collect(data.Addresses())[0].Addr())
}

func TestDataBuilderSnapshotsOutput(t *testing.T) {
	data := NewLocalData(NewData(DataParams{
		Addresses: []Address{{
			Kind:            AddressKindNode,
			NodeAddressType: addressing.NodeInternalIP,
			Prefix:          netip.MustParsePrefix("10.0.0.1/32"),
		}},
		Labels:      map[string]string{"phase": "original"},
		Annotations: map[string]string{"phase": "original"},
	}), LocalNodeInfo{ProviderID: "original"})
	builder := NewDataBuilder(data)
	published := builder.Build()

	builder.SetLabels(map[string]string{"phase": "mutated"})
	builder.SetAnnotation("phase", "mutated")
	builder.SetAddress(
		AddressKindNode,
		addressing.NodeInternalIP,
		false,
		netip.MustParseAddr("10.0.0.2"),
	)
	builder.UpdateLocal(func(info *LocalNodeInfo) {
		info.ProviderID = "mutated"
	})

	require.Equal(t, "original", mustGet(published.Label("phase")))
	require.Equal(t, "original", mustGet(published.Annotation("phase")))
	require.Equal(t, netip.MustParseAddr("10.0.0.1"),
		slices.Collect(published.Addresses())[0].Addr())
	local, found := published.Local()
	require.True(t, found)
	require.Equal(t, "original", local.ProviderID)
}

func TestDataBuilderRetainsPointerForNoop(t *testing.T) {
	data := NewLocalData(NewData(DataParams{
		ClusterID:   1,
		Labels:      map[string]string{"key": "value"},
		Annotations: map[string]string{"key": "value"},
	}), LocalNodeInfo{ProviderID: "provider"})
	builder := NewDataBuilder(data)

	builder.SetClusterID(1)
	builder.SetLabels(map[string]string{"key": "value"})
	builder.SetAnnotation("key", "value")
	builder.UpdateLocal(func(info *LocalNodeInfo) {
		info.ProviderID = "provider"
	})
	require.Same(t, data, builder.Build())

	// A sequence which changes and then restores a value is also a no-op.
	builder.SetClusterID(2)
	builder.SetClusterID(1)
	require.Same(t, data, builder.Build())

	builder.SetClusterID(2)
	updated := builder.Build()
	require.NotSame(t, data, updated)
	require.Same(t, updated, builder.Build())
}

func TestDataBuilderSnapshotDoesNotRebase(t *testing.T) {
	data := NewData(DataParams{ClusterID: 1})
	builder := NewDataBuilder(data)
	builder.SetClusterID(2)

	snapshot := builder.Snapshot()
	require.Equal(t, uint32(2), snapshot.ClusterID())
	builder.SetClusterID(1)

	require.Same(t, data, builder.Build())
}

func TestDataNormalizesAddresses(t *testing.T) {
	data := NewData(DataParams{Addresses: []Address{
		{Kind: AddressKindNode, Prefix: netip.MustParsePrefix("10.0.0.1/24")},
		{Kind: AddressKindAllocation, Prefix: netip.MustParsePrefix("10.1.1.1/16")},
		{Kind: AddressKindHealth},
	}})

	require.Equal(t, []Address{
		{Kind: AddressKindNode, Prefix: netip.MustParsePrefix("10.0.0.1/32")},
		{
			Kind: AddressKindAllocation, Prefix: netip.MustParsePrefix("10.1.0.0/16"),
			Primary: true,
		},
	}, slices.Collect(data.Addresses()))
}

func TestDataBuilderUsesFirstAcceptedAllocationCIDRAsPrimary(t *testing.T) {
	builder := NewDataBuilder(NewData(DataParams{}))
	builder.SetAllocationCIDRs(
		false,
		netip.Prefix{},
		netip.MustParsePrefix("2001:db8::/64"),
		netip.MustParsePrefix("10.0.0.0/24"),
	)

	require.Equal(t, []Address{{
		Kind: AddressKindAllocation, Prefix: netip.MustParsePrefix("10.0.0.0/24"),
		Primary: true,
	}}, slices.Collect(builder.Build().Addresses()))
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
	n := New(NewData(DataParams{
		Name: "node-1",
		Addresses: []Address{
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
	}))

	wire := n.ToKVStoreNode()
	require.Equal(t, netip.MustParsePrefix("10.1.0.0/16"),
		wire.IPv4AllocCIDR.Prefix.Prefix)
	require.Equal(t, []nodeTypes.Prefix{
		nodeTypes.PrefixFrom(netip.MustParsePrefix("10.2.0.0/16")),
	}, wire.IPv4SecondaryAllocCIDRs)
}
