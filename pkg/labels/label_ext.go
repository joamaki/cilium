// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package labels

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/option"
)

//
// Convenience methods for 'Label'
//

const (
	// SourceDelimiter is the delimiter used in the label keys.
	SourceDelimiter = ":"

	// PathDelimiter is the delimiter used in the labels paths.
	PathDelimiter = "."

	// IDNameHost is the label used for the hostname ID.
	IDNameHost = "host"

	// IDNameRemoteNode is the label used to describe the
	// ReservedIdentityRemoteNode
	IDNameRemoteNode = "remote-node"

	// IDNameWorld is the label used for the world ID.
	IDNameWorld = "world"

	// IDNameWorldIPv4 is the label used for the world-ipv4 ID, to distinguish
	// it from world-ipv6 in dual-stack mode.
	IDNameWorldIPv4 = "world-ipv4"

	// IDNameWorldIPv6 is the label used for the world-ipv6 ID, to distinguish
	// it from world-ipv4 in dual-stack mode.
	IDNameWorldIPv6 = "world-ipv6"

	// IDNameCluster is the label used to identify an unspecified endpoint
	// inside the cluster
	IDNameCluster = "cluster"

	// IDNameHealth is the label used for the local cilium-health endpoint
	IDNameHealth = "health"

	// IDNameInit is the label used to identify any endpoint that has not
	// received any labels yet.
	IDNameInit = "init"

	// IDNameKubeAPIServer is the label used to identify the kube-apiserver. It
	// is part of the reserved identity 7 and it is also used in conjunction
	// with IDNameHost if the kube-apiserver is running on the local host.
	IDNameKubeAPIServer = "kube-apiserver"

	// IDNameIngress is the label used to identify Ingress proxies. It is part
	// of the reserved identity 8.
	IDNameIngress = "ingress"

	// IDNameNone is the label used to identify no endpoint or other L3 entity.
	// It will never be assigned and this "label" is here for consistency with
	// other Entities.
	IDNameNone = "none"

	// IDNameUnmanaged is the label used to identify unmanaged endpoints
	IDNameUnmanaged = "unmanaged"

	// IDNameUnknown is the label used to to identify an endpoint with an
	// unknown identity.
	IDNameUnknown = "unknown"
)

var (
	// LabelHealth is the label used for health.
	LabelHealth = NewLabels(NewLabel(IDNameHealth, "", LabelSourceReserved))

	// LabelHost is the label used for the host endpoint.
	LabelHost = NewLabels(NewLabel(IDNameHost, "", LabelSourceReserved))

	// LabelWorld is the label used for world.
	LabelWorld = NewLabels(NewLabel(IDNameWorld, "", LabelSourceReserved))

	// LabelWorldIPv4 is the label used for world-ipv4.
	LabelWorldIPv4 = NewLabels(NewLabel(IDNameWorldIPv4, "", LabelSourceReserved))

	// LabelWorldIPv6 is the label used for world-ipv6.
	LabelWorldIPv6 = NewLabels(NewLabel(IDNameWorldIPv6, "", LabelSourceReserved))

	// LabelRemoteNode is the label used for remote nodes.
	LabelRemoteNode = NewLabels(NewLabel(IDNameRemoteNode, "", LabelSourceReserved))

	// LabelKubeAPIServer is the label used for the kube-apiserver. See comment
	// on IDNameKubeAPIServer.
	LabelKubeAPIServer = NewLabels(NewLabel(IDNameKubeAPIServer, "", LabelSourceReserved))

	// LabelKubeAPIServerExt is the extended kube-apiserver label set.
	LabelKubeAPIServerExt = NewLabels(
		NewLabel(IDNameKubeAPIServer, "", LabelSourceReserved),
		NewLabel(IDNameWorld, "", LabelSourceReserved),
	)

	// LabelIngress is the label used for Ingress proxies. See comment
	// on IDNameIngress.
	LabelIngress = NewLabels(NewLabel(IDNameIngress, "", LabelSourceReserved))

	// Exported to access from tests in other packages.
	WorldLabel   = NewLabel(IDNameWorld, "", LabelSourceReserved)
	WorldLabelV4 = NewLabel(IDNameWorldIPv4, "", LabelSourceReserved)
	WorldLabelV6 = NewLabel(IDNameWorldIPv6, "", LabelSourceReserved)
)

// LabelKeyFixedIdentity is the label that can be used to define a fixed
// identity.
const LabelKeyFixedIdentity = "io.cilium.fixed-identity"

const (
	// LabelSourceUnspec is a label with unspecified source
	LabelSourceUnspec = "unspec"

	// LabelSourceAny is a label that matches any source
	LabelSourceAny = "any"

	// LabelSourceAnyKeyPrefix is prefix of a "any" label
	LabelSourceAnyKeyPrefix = LabelSourceAny + SourceDelimiter

	// LabelSourceK8s is a label imported from Kubernetes
	LabelSourceK8s = "k8s"

	// LabelSourceK8sKeyPrefix is prefix of a Kubernetes label
	LabelSourceK8sKeyPrefix = LabelSourceK8s + SourceDelimiter

	// LabelSourceContainer is a label imported from the container runtime
	LabelSourceContainer = "container"

	// LabelSourceCNI is a label imported from the CNI plugin
	LabelSourceCNI = "cni"

	// LabelSourceReserved is the label source for reserved types.
	LabelSourceReserved = "reserved"

	// LabelSourceCIDR is the label source for generated CIDRs.
	LabelSourceCIDR = "cidr"

	// LabelSourceCIDRGroup is the label source used for labels from CIDRGroups
	LabelSourceCIDRGroup = "cidrgroup"

	// LabelSourceNode is the label source for remote-nodes.
	LabelSourceNode = "node"

	// LabelSourceNodeKeyPrefix is prefix of a node label
	LabelSourceNodeKeyPrefix = LabelSourceNode + SourceDelimiter

	// LabelSourceFQDN is the label source for IPs resolved by fqdn lookups
	LabelSourceFQDN = "fqdn"

	// LabelSourceGenerated is an identity label generated by the agent.
	LabelSourceGenerated = "gen"

	// LabelSourceReservedKeyPrefix is the prefix of a reserved label
	LabelSourceReservedKeyPrefix = LabelSourceReserved + SourceDelimiter

	// LabelSourceCIDRGroupKeyPrefix is the source as a k8s selector key prefix
	LabelSourceCIDRGroupKeyPrefix = LabelSourceCIDRGroup + SourceDelimiter

	// CIDRGroupEncodedSep is the separator for encoded key+value CIDRGroup labels.
	// Safe because K8s label keys/values cannot contain "+".
	CIDRGroupEncodedSep = "+"

	// LabelSourceDirectory is the label source for policies read from files
	LabelSourceDirectory = "directory"
)

// EncodedCIDRGroupLabel builds a label with the value baked into the key,
// used for collision-free matching of CIDRGroup labels.
func EncodedCIDRGroupLabel(key, val, source string) Label {
	return MakeLabel(key+CIDRGroupEncodedSep+val, "", source)
}

// NewLabel returns a new label from the given key, value and source. If source is empty,
// the default value will be LabelSourceUnspec. If key starts with '$', the source
// will be overwritten with LabelSourceReserved. If key contains ':', the value
// before ':' will be used as source if given source is empty, otherwise the value before
// ':' will be deleted and unused.
func NewLabel(key string, value string, source string) Label {
	var src string
	src, key = ParseSource(key, ':')
	if source == "" {
		if src == "" {
			source = LabelSourceUnspec
		} else {
			source = src
		}
	}
	if src == LabelSourceReserved && key == "" {
		key = value
		value = ""
	}

	var l Label
	if source == LabelSourceCIDR {
		c, err := LabelToPrefix(key)
		if err != nil {
			// slogloggercheck: it's safe to use the default logger here as it has been initialized by the program up to this point.
			logging.DefaultSlogLogger.Error("Failed to parse CIDR label: invalid prefix.",
				logfields.Error, err,
				logfields.Key, key,
			)
			l = MakeLabel(key, value, source)
		} else {
			l = MakeCIDRLabel(key, value, source, &c)
		}
	} else {
		l = MakeLabel(key, value, source)
	}
	return l
}

func (l Label) DeepEqual(other *Label) bool {
	return other != nil && l == *other
}

func (l Label) CIDR() *netip.Prefix {
	return l.rep().cidr
}

// GetCIDRPrefix returns the cidr of the Label, or nil if none.
func (l Label) GetCIDRPrefix() *netip.Prefix {
	return l.CIDR()
}

// Has returns true label L contains target.
// target may be "looser" w.r.t source or cidr, i.e.
// "k8s:foo=bar".Has("any:foo=bar") is true
// "any:foo=bar".Has("k8s:foo=bar") is false
// "cidr:10.0.0.1/32".Has("cidr:10.0.0.0/24") is true
func (l Label) Has(target Label) bool {
	return l.HasKey(target) && l.Value() == target.Value()
}

// HasKey returns true if l has target's key.
// target may be "looser" w.r.t source or cidr, i.e.
// "k8s:foo=bar".HasKey("any:foo") is true
// "any:foo=bar".HasKey("k8s:foo") is false
// "cidr:10.0.0.1/32".HasKey("cidr:10.0.0.0/24") is true
// "cidr:10.0.0.0/24".HasKey("cidr:10.0.0.1/32") is false
func (l Label) HasKey(target Label) bool {
	if !target.IsAnySource() && l.Source() != target.Source() {
		return false
	}

	// Do cidr-aware matching if both sources are "cidr".
	if target.Source() == LabelSourceCIDR && l.Source() == LabelSourceCIDR {
		tc := target.CIDR()
		if tc == nil {
			v, err := LabelToPrefix(target.Key())
			if err == nil {
				tc = &v
			}
		}
		lc := l.CIDR()
		if lc == nil {
			v, err := LabelToPrefix(l.Key())
			if err == nil {
				lc = &v
			}
		}
		if tc != nil && lc != nil && tc.Bits() <= lc.Bits() && tc.Contains(lc.Addr()) {
			return true
		}
	}

	return l.Key() == target.Key()
}

func (l Label) HasCIDR(cidr netip.Prefix) bool {
	if l.Source() != LabelSourceCIDR {
		return false
	}
	lc := l.CIDR()
	if lc == nil {
		v, err := LabelToPrefix(l.Key())
		if err == nil {
			lc = &v
		}
	}
	return lc != nil && cidr.Bits() <= lc.Bits() && cidr.Contains(lc.Addr())
}

// IsValid returns true if Key != "".
func (l Label) IsValid() bool {
	return l.Key() != ""
}

// IsAnySource return if the label was set with source "any".
func (l Label) IsAnySource() bool {
	return l.Source() == LabelSourceAny
}

// IsReservedSource return if the label was set with source "Reserved".
func (l Label) IsReservedSource() bool {
	return l.Source() == LabelSourceReserved
}

// GetExtendedKey returns the key of a label with the source encoded.
func (l Label) GetExtendedKey() string {
	return l.Source() + SourceDelimiter + l.Key()
}

func LabelToPrefix(key string) (netip.Prefix, error) {
	prefixStr := strings.Replace(key, "-", ":", -1)
	pfx, err := netip.ParsePrefix(prefixStr)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("failed to parse label prefix %s: %w", key, err)
	}
	return pfx, nil
}

// getCIDRLabel returns a Label representation of the given prefix. Should not be
// called for zero length prefixes, which need to be represented with a world label.
//
// For IPv6 addresses, it converts ":" into "-" because endpoint selectors do
// not support colons inside the name section of a label.
func getCIDRLabel(prefix netip.Prefix) Label {
	ipv6 := prefix.Addr().Is6()
	ipStr := prefix.Masked().Addr().String()
	prefixLen := prefix.Bits()

	var str strings.Builder
	str.Grow(1 + len(ipStr) + 1 + 2 + 1)

	if ipv6 {
		for i := range len(ipStr) {
			if ipStr[i] == ':' {
				if i == 0 {
					str.WriteByte('0')
					str.WriteByte('-')
					continue
				}
				if i == len(ipStr)-1 {
					str.WriteByte('-')
					str.WriteByte('0')
					continue
				}
				str.WriteByte('-')
			} else {
				str.WriteByte(ipStr[i])
			}
		}
	} else {
		str.WriteString(ipStr)
	}
	str.WriteByte('/')
	str.WriteString(strconv.Itoa(prefixLen))

	return MakeCIDRLabel(str.String(), "", LabelSourceCIDR, &prefix)
}

// IPStringToLabel parses a string and returns it as a single CIDR label.
// World label is not added, but a zero-length prefix is represented as the
// appropriate world label.
func IPStringToLabel(ip string) (Label, error) {
	var prefix netip.Prefix
	i := strings.LastIndexByte(ip, '/')
	if i < 0 {
		parsedIP, parseErr := netip.ParseAddr(ip)
		if parseErr != nil {
			return EmptyLabel, fmt.Errorf("%q is not an IP address: %w", ip, parseErr)
		}
		var err error
		prefix, err = parsedIP.Prefix(parsedIP.BitLen())
		if err != nil {
			return EmptyLabel, fmt.Errorf("%q cannot get prefix: %w", ip, err)
		}
	} else {
		var err error
		prefix, err = netip.ParsePrefix(ip)
		if err != nil {
			return EmptyLabel, fmt.Errorf("%q is not a CIDR: %w", ip, err)
		}
	}

	if prefix.Bits() > 0 {
		return getCIDRLabel(prefix), nil
	}
	return getWorldLabel(prefix.Addr()), nil
}

// ErrLabelNotCIDR is returned when a label is not a value-less CIDR label.
var ErrLabelNotCIDR = errors.New("Label is not a CIDR label")

// ToCIDRString reverses IPStringToLabel for testing purposes, mainly.
func (l Label) ToCIDRString() (string, error) {
	if l.CIDR() == nil || l.Source() != LabelSourceCIDR || l.Value() != "" {
		return "", ErrLabelNotCIDR
	}
	return l.CIDR().String(), nil
}

// GetCIDRLabels turns a CIDR into labels including the CIDR-specific label and
// the appropriate world label. For a zero-length prefix only the world label is
// returned.
func GetCIDRLabels(prefix netip.Prefix) Labels {
	lbls := NewLabels()
	if prefix.Bits() > 0 {
		lbls = lbls.Add(getCIDRLabel(prefix))
	}
	return lbls.AddWorldLabel(prefix.Addr())
}

// GetCIDRLabelArray turns a CIDR into labels and returns them as a LabelArray.
func GetCIDRLabelArray(prefix netip.Prefix) LabelArray {
	return ToLabelArray(GetCIDRLabels(prefix))
}

func getWorldLabel(addr netip.Addr) Label {
	switch {
	case addr.Is4() && option.Config.EnableIPv6:
		return WorldLabelV4
	case addr.Is6() && option.Config.EnableIPv4:
		return WorldLabelV6
	}
	return WorldLabel
}

// FormatForKVStore returns the label as a formatted string, ending in
// a semicolon
//
// DO NOT BREAK THE FORMAT OF THIS. THE RETURNED STRING IS USED AS
// PART OF THE KEY IN THE KEY-VALUE STORE.
//
// Non-pointer receiver allows this to be called on a value in a map.
func (l Label) FormatForKVStore() []byte {
	// We don't care if the values already have a '='.
	//
	// We absolutely care that the final character is a semi-colon.
	// Identity allocation in the kvstore depends on this (see
	// kvstore.prefixMatchesKey())
	b := make([]byte, 0, len(l.Source())+len(l.Key())+len(l.Value())+3)
	buf := bytes.NewBuffer(b)
	l.FormatForKVStoreInto(buf)
	return buf.Bytes()
}

// FormatForKVStoreInto writes the label as a formatted string, ending in
// a semicolon into buf.
//
// DO NOT BREAK THE FORMAT OF THIS. THE RETURNED STRING IS USED AS
// PART OF THE KEY IN THE KEY-VALUE STORE.
//
// Non-pointer receiver allows this to be called on a value in a map.
func (l Label) FormatForKVStoreInto(buf *bytes.Buffer) {
	buf.WriteString(l.Source())
	buf.WriteRune(':')
	buf.WriteString(l.Key())
	buf.WriteRune('=')
	buf.WriteString(l.Value())
	buf.WriteRune(';')
}

func (l Label) BuildString(sb *strings.Builder) {
	sb.WriteString(l.Source())
	sb.WriteString(":")
	sb.WriteString(l.Key())
	value := l.Value()
	if len(value) != 0 {
		sb.WriteString("=")
		sb.WriteString(value)
	}
}

func (l Label) BuildBytes(buf *bytes.Buffer) {
	buf.WriteString(l.Source())
	buf.WriteString(":")
	buf.WriteString(l.Key())
	value := l.Value()
	if len(value) != 0 {
		buf.WriteString("=")
		buf.WriteString(value)
	}
}
