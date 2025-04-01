// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package labels

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cilium/cilium/pkg/option"
)

func assertLabelFields(t *testing.T, want, got Label) {
	t.Helper()
	assert.Equal(t, want.Key(), got.Key())
	assert.Equal(t, want.Value(), got.Value())
	assert.Equal(t, want.Source(), got.Source())
}

func TestParseLabelParity(t *testing.T) {
	tests := []struct {
		str string
		out Label
	}{
		{"source1:key1=value1", NewLabel("key1", "value1", "source1")},
		{"key1=value1", NewLabel("key1", "value1", LabelSourceUnspec)},
		{"value1", NewLabel("value1", "", LabelSourceUnspec)},
		{"source1:key1", NewLabel("key1", "", "source1")},
		{"source1:key1==value1", NewLabel("key1", "=value1", "source1")},
		{"source::key1=value1", NewLabel("::key1", "value1", "source")},
		{"$key1=value1", NewLabel("key1", "value1", LabelSourceReserved)},
		{":2foo", NewLabel("2foo", "", LabelSourceUnspec)},
		{":3foo=", NewLabel("3foo", "", LabelSourceUnspec)},
		{"reserved:=key", NewLabel("key", "", LabelSourceReserved)},
		{"4blah=:foo=", NewLabel("foo", "", "4blah=")},
		{"5blah::foo=", NewLabel("::foo", "", "5blah")},
		{"6foo==", NewLabel("6foo", "=", LabelSourceUnspec)},
		{"7foo=bar", NewLabel("7foo", "bar", LabelSourceUnspec)},
		{"k8s:foo=bar:", NewLabel("foo", "bar:", LabelSourceK8s)},
		{LabelSourceReservedKeyPrefix + "host", NewLabel("host", "", LabelSourceReserved)},
	}
	for _, tt := range tests {
		t.Run(tt.str, func(t *testing.T) {
			assertLabelFields(t, tt.out, ParseLabel(tt.str))
		})
	}
}

func TestLabelsHasUsesCiliumSourceDelimiter(t *testing.T) {
	lbls := NewLabels(
		NewLabel("foo", "bar", LabelSourceK8s),
		NewLabel("user", "bob", LabelSourceContainer),
		NewLabel("10.1.2.0/24", "", LabelSourceCIDR),
	)

	for key, expected := range map[string]bool{
		"foo":              true,
		"any:foo":          true,
		"any.foo":          false,
		"k8s:foo":          true,
		"k8s.foo":          false,
		"container:user":   true,
		"container.user":   false,
		"container:foo":    false,
		"cidr:10.1.2.0/24": true,
		"cidr:10.1.0.0/22": true,
		"cidr:10.1.2.0/25": false,
	} {
		assert.Equal(t, expected, lbls.Has(key), key)
	}
}

func TestLabelsSemanticContainsAndHasLabel(t *testing.T) {
	lbls := NewLabels(
		NewLabel("foo", "bar", LabelSourceK8s),
		NewLabel("10.0.0.1/32", "", LabelSourceCIDR),
	)

	assert.True(t, lbls.HasLabel(NewLabel("foo", "bar", LabelSourceAny)))
	assert.False(t, lbls.HasLabel(NewLabel("foo", "baz", LabelSourceAny)))
	assert.True(t, lbls.HasLabel(NewLabel("10.0.0.0/24", "", LabelSourceCIDR)))
	assert.False(t, lbls.HasLabel(NewLabel("10.0.0.2/32", "", LabelSourceCIDR)))
	assert.False(t, NewLabel("foo", "bar", LabelSourceAny).DeepEqual(new(NewLabel("foo", "bar", LabelSourceK8s))))

	assert.True(t, lbls.Contains(NewLabels(NewLabel("foo", "bar", LabelSourceAny))))
	assert.False(t, lbls.Contains(NewLabels(NewLabel("foo", "baz", LabelSourceAny))))
	assert.True(t, lbls.Contains(NewLabels(NewLabel("10.0.0.0/24", "", LabelSourceCIDR))))
}

func TestLabelsEqualStrictForOverflow(t *testing.T) {
	left := make([]Label, 0, smallLabelsSize+1)
	right := make([]Label, 0, smallLabelsSize+1)
	for i := range smallLabelsSize + 1 {
		key := string(rune('a' + i))
		source := LabelSourceK8s
		if i == smallLabelsSize {
			source = LabelSourceAny
		}
		left = append(left, NewLabel(key, "value", source))
		right = append(right, NewLabel(key, "value", LabelSourceK8s))
	}

	assert.False(t, NewLabels(left...).Equal(NewLabels(right...)))
}

func TestMap2LabelsCompactsParsedDuplicateKeys(t *testing.T) {
	lbls := Map2Labels(map[string]string{
		"k8s:foo":       "bar",
		"container:foo": "baz",
	}, "")

	assert.Equal(t, 1, lbls.Len())
	_, found := lbls.GetLabel("foo")
	assert.True(t, found)
}

func TestCIDRHelpersParity(t *testing.T) {
	enableIPv4, enableIPv6 := option.Config.EnableIPv4, option.Config.EnableIPv6
	t.Cleanup(func() {
		option.Config.EnableIPv4, option.Config.EnableIPv6 = enableIPv4, enableIPv6
	})
	option.Config.EnableIPv4 = true
	option.Config.EnableIPv6 = true

	lbl, err := IPStringToLabel("0.0.0.0/0")
	require.NoError(t, err)
	assert.Equal(t, "reserved:world-ipv4", lbl.String())

	lbl, err = IPStringToLabel("::/0")
	require.NoError(t, err)
	assert.Equal(t, "reserved:world-ipv6", lbl.String())

	lbl, err = IPStringToLabel("192.0.2.3/24")
	require.NoError(t, err)
	assert.Equal(t, "cidr:192.0.2.0/24", lbl.String())

	lbls := GetCIDRLabels(netip.MustParsePrefix("192.0.2.3/24"))
	assert.True(t, lbls.HasLabel(NewLabel("192.0.2.0/24", "", LabelSourceCIDR)))
	assert.True(t, lbls.HasLabel(NewLabel(IDNameWorldIPv4, "", LabelSourceReserved)))
	assert.Equal(t,
		[]string{"cidr:192.0.2.0/24", "reserved:world-ipv4"},
		lbls.GetPrintableModel(),
	)

	zero := GetCIDRLabels(netip.MustParsePrefix("0.0.0.0/0"))
	assert.True(t, NewLabels(WorldLabelV4).Equal(zero))

	array := GetCIDRLabelArray(netip.MustParsePrefix("2001:db8::/64"))
	require.Len(t, array, 2)
	assert.Equal(t, "cidr:2001-db8--0/64", array[0].String())
	assert.Equal(t, "reserved:world-ipv6", array[1].String())
}

func TestCIDRPrefixSurvivesInterning(t *testing.T) {
	uncached := MakeLabel("10.0.0.1/32", "", LabelSourceCIDR)
	require.NotNil(t, uncached.CIDR())

	parsed := ParseLabel("cidr:10.0.0.1/32")
	require.NotNil(t, parsed.CIDR())
	assert.Equal(t, uncached.CIDR().String(), parsed.CIDR().String())
}

func TestEncodedCIDRGroupLabel(t *testing.T) {
	lbl := EncodedCIDRGroupLabel("app", "foo", LabelSourceCIDRGroup)
	assert.Equal(t, "app+foo", lbl.Key())
	assert.Equal(t, "", lbl.Value())
	assert.Equal(t, LabelSourceCIDRGroup, lbl.Source())
}

func TestLabelsJSONMapShape(t *testing.T) {
	lbls := NewLabels(
		NewLabel("b", "2", LabelSourceK8s),
		NewLabel("a", "1", LabelSourceK8s),
	)

	b, err := json.Marshal(lbls)
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":{"key":"a","value":"1","source":"k8s"},"b":{"key":"b","value":"2","source":"k8s"}}`, string(b))

	var decoded Labels
	require.NoError(t, json.Unmarshal(b, &decoded))
	assert.True(t, lbls.Equal(decoded))
}
