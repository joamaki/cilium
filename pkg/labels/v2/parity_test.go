// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package v2

import (
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	original "github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/option"
)

func assertLabelFields(t *testing.T, want, got Label) {
	t.Helper()
	assert.Equal(t, want.Key(), got.Key())
	assert.Equal(t, want.Value(), got.Value())
	assert.Equal(t, want.Source(), got.Source())
}

func assertMatchesOriginalLabel(t *testing.T, want original.Label, got Label) {
	t.Helper()
	assert.Equal(t, want.Key, got.Key())
	assert.Equal(t, want.Value, got.Value())
	assert.Equal(t, want.Source, got.Source())
	assert.Equal(t, want.String(), got.String())
}

func TestParseLabelMatchesOriginal(t *testing.T) {
	for _, str := range []string{
		"source1:key1=value1",
		"key1=value1",
		"value1",
		"source1:key1",
		"source1:key1==value1",
		"source::key1=value1",
		"$key1=value1",
		":2foo",
		":3foo=",
		"reserved:=key",
		"4blah=:foo=",
		"5blah::foo=",
		"6foo==",
		"7foo=bar",
		"k8s:foo=bar:",
		original.LabelSourceReservedKeyPrefix + "host",
	} {
		t.Run(str, func(t *testing.T) {
			assertMatchesOriginalLabel(t, original.ParseLabel(str), ParseLabel(str))
			assertMatchesOriginalLabel(t, original.ParseSelectLabel(str), ParseSelectLabel(str))
		})
	}
}

func TestNewSourceEncodedLabelKeyMatchesOriginal(t *testing.T) {
	for _, key := range []string{
		"foo",
		"k8s:foo",
		"reserved:host",
		"cidr:10.0.0.0/8",
		"foo=bar",
	} {
		t.Run(key, func(t *testing.T) {
			assert.Equal(t,
				original.NewSourceEncodedLabelKey(original.LabelSourceK8sKeyPrefix, key),
				NewSourceEncodedLabelKey(LabelSourceK8sKeyPrefix, key),
			)
		})
	}
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

func TestCIDRHelpersMatchOriginal(t *testing.T) {
	enableIPv4, enableIPv6 := option.Config.EnableIPv4, option.Config.EnableIPv6
	t.Cleanup(func() {
		option.Config.EnableIPv4, option.Config.EnableIPv6 = enableIPv4, enableIPv6
	})
	option.Config.EnableIPv4 = true
	option.Config.EnableIPv6 = true

	for _, ip := range []string{
		"0.0.0.0/0",
		"::/0",
		"192.0.2.3",
		"192.0.2.3/24",
		"2001:db8::1/128",
		"2001:db8::1/64",
	} {
		t.Run(ip, func(t *testing.T) {
			want, wantErr := original.IPStringToLabel(ip)
			got, gotErr := IPStringToLabel(ip)
			require.Equal(t, wantErr, gotErr)
			if wantErr == nil {
				assertMatchesOriginalLabel(t, want, got)
			}
		})
	}

	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/0"),
		netip.MustParsePrefix("::/0"),
		netip.MustParsePrefix("192.0.2.3/24"),
		netip.MustParsePrefix("2001:db8::1/64"),
	} {
		t.Run(prefix.String(), func(t *testing.T) {
			assert.Equal(t,
				original.GetCIDRLabels(prefix).GetPrintableModel(),
				GetCIDRLabels(prefix).GetPrintableModel(),
			)
		})
	}
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
	orig := original.NewLabelsFromModel([]string{
		"k8s:b=2",
		"k8s:a=1",
	})

	b, err := json.Marshal(lbls)
	require.NoError(t, err)
	origB, err := json.Marshal(orig)
	require.NoError(t, err)
	assert.JSONEq(t, string(origB), string(b))

	var decoded Labels
	require.NoError(t, json.Unmarshal(b, &decoded))
	assert.True(t, lbls.Equal(decoded))
}
