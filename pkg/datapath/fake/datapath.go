// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package fake

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/cilium/cilium/pkg/datapath"
	"github.com/cilium/cilium/pkg/datapath/loader/metrics"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/ipcache"
	"github.com/cilium/cilium/pkg/testutils/mockmaps"
)

var _ datapath.Datapath = (*FakeDatapath)(nil)

type FakeDatapath struct {
	node            *FakeNodeHandler
	nodeAddressing  types.NodeAddressing
	loader          datapath.Loader
	lbmap           *mockmaps.LBMockMap
	ipcacheListener IPCacheListener
}

// NewDatapath returns a new fake datapath
func NewDatapath() *FakeDatapath {
	return &FakeDatapath{
		node:           NewNodeHandler(),
		nodeAddressing: NewNodeAddressing(),
		loader:         &fakeLoader{},
		lbmap:          mockmaps.NewLBMockMap(),
	}
}

// Node returns a fake handler for node events
func (f *FakeDatapath) Node() datapath.NodeHandler {
	return f.node
}

func (f *FakeDatapath) FakeNode() *FakeNodeHandler {
	return f.node
}

// LocalNodeAddressing returns a fake node addressing implementation of the
// local node
func (f *FakeDatapath) LocalNodeAddressing() types.NodeAddressing {
	return f.nodeAddressing
}

// WriteNodeConfig pretends to write the datapath configuration to the writer.
func (f *FakeDatapath) WriteNodeConfig(io.Writer, *datapath.LocalNodeConfiguration) error {
	return nil
}

// WriteNetdevConfig pretends to write the netdev configuration to a writer.
func (f *FakeDatapath) WriteNetdevConfig(io.Writer, datapath.DeviceConfiguration) error {
	return nil
}

// WriteTemplateConfig pretends to write the endpoint configuration to a writer.
func (f *FakeDatapath) WriteTemplateConfig(io.Writer, datapath.EndpointConfiguration) error {
	return nil
}

// WriteEndpointConfig pretends to write the endpoint configuration to a writer.
func (f *FakeDatapath) WriteEndpointConfig(io.Writer, datapath.EndpointConfiguration) error {
	return nil
}

func (f *FakeDatapath) InstallProxyRules(context.Context, uint16, bool, string) error {
	return nil
}

func (f *FakeDatapath) SupportsOriginalSourceAddr() bool {
	return false
}

func (f *FakeDatapath) InstallRules(ctx context.Context, ifName string, quiet, install bool) error {
	return nil
}

func (m *FakeDatapath) GetProxyPort(name string) uint16 {
	return 0
}

func (m *FakeDatapath) InstallNoTrackRules(IP string, port uint16, ipv6 bool) error {
	return nil
}

func (m *FakeDatapath) RemoveNoTrackRules(IP string, port uint16, ipv6 bool) error {
	return nil
}

func (f *FakeDatapath) Loader() datapath.Loader {
	return f.loader
}

func (f *FakeDatapath) WireguardAgent() datapath.WireguardAgent {
	return nil
}

func (f *FakeDatapath) Procfs() string {
	return "/proc"
}

func (f *FakeDatapath) LBMap() types.LBMap {
	return f.lbmap
}

func (f *FakeDatapath) LBMockMap() *mockmaps.LBMockMap {
	return f.lbmap
}

// Loader is an interface to abstract out loading of datapath programs.
type fakeLoader struct {
}

func (f *fakeLoader) CompileAndLoad(ctx context.Context, ep datapath.Endpoint, stats *metrics.SpanStat) error {
	fmt.Printf(">>> CompileAndLoad(ep=%#v)\n", ep)
	return nil
}

func (f *fakeLoader) CompileOrLoad(ctx context.Context, ep datapath.Endpoint, stats *metrics.SpanStat) error {
	fmt.Printf(">>> CompileOrLoad(ep=%#v)\n", ep)
	return nil
}

func (f *fakeLoader) ReloadDatapath(ctx context.Context, ep datapath.Endpoint, stats *metrics.SpanStat) error {
	fmt.Printf(">>> ReloadDatapath(ep=%#v)\n", ep)
	return nil
}

func (f *fakeLoader) EndpointHash(cfg datapath.EndpointConfiguration) (string, error) {
	// FIXME proper hash
	return cfg.StringID(), nil
}

func (f *fakeLoader) Unload(ep datapath.Endpoint) {
}

func (f *fakeLoader) CallsMapPath(id uint16) string {
	return ""
}

func (f *fakeLoader) CustomCallsMapPath(id uint16) string {
	return ""
}

// Reinitialize does nothing.
func (f *fakeLoader) Reinitialize(ctx context.Context, o datapath.BaseProgramOwner, deviceMTU int, iptMgr datapath.IptablesManager, p datapath.Proxy) error {
	fmt.Printf(">>> Reinitialize datapath\n")
	return nil
}

func (f *FakeDatapath) FakeIPCacheListener() *IPCacheListener {
	return &f.ipcacheListener
}

type IPCacheIdentityChange struct {
	ModType ipcache.CacheModification
	Meta    *ipcache.K8sMetadata
	CIDR    net.IPNet
	OldID   *ipcache.Identity
	NewID   ipcache.Identity
}

type IPCacheListener struct {
	// FIXME need mu
	identityChanges []IPCacheIdentityChange
}

func (l *IPCacheListener) GetIdentityChanges() []IPCacheIdentityChange {
	// FIXME lock & deep copy
	return l.identityChanges
}

func (l *IPCacheListener) OnIPIdentityCacheChange(modType ipcache.CacheModification, cidr net.IPNet,
	oldHostIP, newHostIP net.IP, oldID *ipcache.Identity, newID ipcache.Identity,
	encryptKey uint8, k8sMeta *ipcache.K8sMetadata) {
	fmt.Printf("OnIPIdentityCacheChange invoked\n")
	l.identityChanges = append(l.identityChanges, IPCacheIdentityChange{
		ModType: modType,
		Meta:    k8sMeta,
		CIDR:    cidr,
		OldID:   oldID,
		NewID:   newID,
	})
}

func (l *IPCacheListener) OnIPIdentityCacheGC() {
	fmt.Printf("OnIPIdentityCacheGC invoked\n")
}
