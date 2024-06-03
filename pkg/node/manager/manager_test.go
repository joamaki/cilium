// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fakeTypes "github.com/cilium/cilium/pkg/datapath/fake/types"
	"github.com/cilium/cilium/pkg/datapath/iptables/ipset"
	datapath "github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/node"
	"github.com/cilium/cilium/pkg/node/addressing"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/source"
)

func newFixture(t testing.TB, p NodeManagerParams) *manager {
	if p.DaemonConfig == nil {
		p.DaemonConfig = &option.DaemonConfig{}
	}
	if p.IPSetMgr == nil {
		p.IPSetMgr = newIPSetMock()
	}
	if p.NodeMetrics == nil {
		p.NodeMetrics = NewNodeMetrics()
	}
	if p.Health == nil {
		h, _ := cell.NewSimpleHealth()
		p.Health = h
	}

	var mngr *manager
	h := hive.New(
		cell.Invoke(func(lc cell.Lifecycle, jobs job.Registry, db *statedb.DB) error {
			table, err := node.NewNodesTable(db)
			if err != nil {
				return err
			}
			p.NodesTable = table
			p.Lifecycle = lc
			p.DB = db
			p.Jobs = jobs
			mngr, err = New(p)
			lc.Append(mngr)
			return err
		}),
	)
	require.NoError(t, h.Start(hivetest.Logger(t), context.TODO()), "Start")

	t.Cleanup(func() {
		assert.NoError(t, h.Stop(hivetest.Logger(t), context.TODO()), "Stop")
	})

	return mngr
}

func AddrOrPrefixToIP(ip string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(ip)
	if err != nil {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return netip.Prefix{}, err
		}
		return addr.Prefix(prefix.Bits())
	}

	return prefix, err
}

type ipsetMock struct {
	v4 map[string]struct{}
	v6 map[string]struct{}
}

func newIPSetMock() *ipsetMock {
	return &ipsetMock{
		v4: make(map[string]struct{}),
		v6: make(map[string]struct{}),
	}
}

type ipsetInitializerMock struct{}

func (i *ipsetInitializerMock) InitDone() {
}

func (i *ipsetMock) NewInitializer() ipset.Initializer {
	return &ipsetInitializerMock{}
}

func (i *ipsetMock) AddToIPSet(name string, family ipset.Family, addrs ...netip.Addr) {
	for _, addr := range addrs {
		if name == ipset.CiliumNodeIPSetV4 && family == ipset.INetFamily {
			i.v4[addr.String()] = struct{}{}
		} else if name == ipset.CiliumNodeIPSetV6 && family == ipset.INet6Family {
			i.v6[addr.String()] = struct{}{}
		}
	}
}

func (i *ipsetMock) RemoveFromIPSet(name string, addrs ...netip.Addr) {
	for _, addr := range addrs {
		if name == ipset.CiliumNodeIPSetV4 {
			delete(i.v4, addr.String())
		} else if name == ipset.CiliumNodeIPSetV6 {
			delete(i.v6, addr.String())
		}
	}
}

func ipsetContains(ipsetMgr *ipsetMock, setName string, addr string) (bool, error) {
	switch setName {
	case ipset.CiliumNodeIPSetV4:
		_, found := ipsetMgr.v4[addr]
		return found, nil
	case ipset.CiliumNodeIPSetV6:
		_, found := ipsetMgr.v6[addr]
		return found, nil
	default:
		return false, fmt.Errorf("unexpected ipset name %s", setName)
	}
}

type signalNodeHandler struct {
	EnableNodeAddEvent                    bool
	NodeAddEvent                          chan nodeTypes.Node
	NodeAddEventError                     error
	NodeUpdateEvent                       chan nodeTypes.Node
	NodeUpdateEventError                  error
	EnableNodeUpdateEvent                 bool
	NodeDeleteEvent                       chan nodeTypes.Node
	NodeDeleteEventError                  error
	EnableNodeDeleteEvent                 bool
	NodeValidateImplementationEvent       chan nodeTypes.Node
	NodeValidateImplementationEventError  error
	EnableNodeValidateImplementationEvent bool
}

func newSignalNodeHandler() *signalNodeHandler {
	return &signalNodeHandler{
		NodeAddEvent:                    make(chan nodeTypes.Node, 10),
		NodeUpdateEvent:                 make(chan nodeTypes.Node, 10),
		NodeDeleteEvent:                 make(chan nodeTypes.Node, 10),
		NodeValidateImplementationEvent: make(chan nodeTypes.Node, 4096),
	}
}

func (s *signalNodeHandler) Name() string {
	return "manager_test:signalNodeHandler"
}

func (n *signalNodeHandler) NodeAdd(newNode nodeTypes.Node) error {
	if n.EnableNodeAddEvent {
		n.NodeAddEvent <- newNode
	}
	return n.NodeAddEventError
}

func (n *signalNodeHandler) NodeUpdate(oldNode, newNode nodeTypes.Node) error {
	if n.EnableNodeUpdateEvent {
		n.NodeUpdateEvent <- newNode
	}
	return n.NodeUpdateEventError
}

func (n *signalNodeHandler) NodeDelete(node nodeTypes.Node) error {
	if n.EnableNodeDeleteEvent {
		n.NodeDeleteEvent <- node
	}
	return n.NodeDeleteEventError
}

func (n *signalNodeHandler) AllNodeValidateImplementation() {
}

func (n *signalNodeHandler) NodeValidateImplementation(node nodeTypes.Node) error {
	if n.EnableNodeValidateImplementationEvent {
		n.NodeValidateImplementationEvent <- node
	}
	return n.NodeValidateImplementationEventError
}

func (n *signalNodeHandler) NodeConfigurationChanged(config datapath.LocalNodeConfiguration) error {
	return nil
}

func setup(tb testing.TB) {
	node.SetTestLocalNodeStore()

	tb.Cleanup(func() {
		node.UnsetTestLocalNodeStore()
	})
}

func TestNodeLifecycle(t *testing.T) {
	setup(t)

	dp := newSignalNodeHandler()
	dp.EnableNodeAddEvent = true
	dp.EnableNodeUpdateEvent = true
	dp.EnableNodeDeleteEvent = true

	mngr := newFixture(t, NodeManagerParams{})
	mngr.Subscribe(dp)

	n1 := nodeTypes.Node{Name: "node1", Cluster: "c1", IPAddresses: []nodeTypes.Address{
		{
			Type: addressing.NodeInternalIP,
			IP:   net.ParseIP("10.0.0.1"),
		},
	}}
	mngr.NodeUpdated(n1)

	select {
	case nodeEvent := <-dp.NodeAddEvent:
		require.Equal(t, n1, nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeAdd() event for node1")
	}

	n2 := nodeTypes.Node{Name: "node2", Cluster: "c1", IPAddresses: []nodeTypes.Address{
		{
			Type: addressing.NodeInternalIP,
			IP:   net.ParseIP("10.0.0.2"),
		},
	}}
	mngr.NodeUpdated(n2)

	select {
	case nodeEvent := <-dp.NodeAddEvent:
		require.Equal(t, n2, nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeUpdate() event for node2")
	}

	nodes := mngr.GetNodes()
	n, ok := nodes[n1.Identity()]
	require.True(t, ok)
	require.Equal(t, n1, n)

	mngr.NodeDeleted(n1)
	select {
	case nodeEvent := <-dp.NodeDeleteEvent:
		require.Equal(t, n1, nodeEvent)
	case nodeEvent := <-dp.NodeAddEvent:
		t.Errorf("Unexpected NodeAdd() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeDelete() event for node1")
	}
	nodes = mngr.GetNodes()
	_, ok = nodes[n1.Identity()]
	require.False(t, ok)
}

func TestMultipleSources(t *testing.T) {
	setup(t)

	dp := newSignalNodeHandler()
	dp.EnableNodeAddEvent = true
	dp.EnableNodeUpdateEvent = true
	dp.EnableNodeDeleteEvent = true
	mngr := newFixture(t, NodeManagerParams{})
	mngr.Subscribe(dp)

	n1k8s := nodeTypes.Node{Name: "node1", Cluster: "c1", Source: source.Kubernetes, IPAddresses: []nodeTypes.Address{
		{
			Type: addressing.NodeInternalIP,
			IP:   net.ParseIP("10.0.0.1"),
		},
	}}
	mngr.NodeUpdated(n1k8s)
	select {
	case nodeEvent := <-dp.NodeAddEvent:
		require.Equal(t, n1k8s, nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeAdd() event for node1")
	}

	// agent can overwrite kubernetes
	n1agent := nodeTypes.Node{Name: "node1", Cluster: "c1", Source: source.Local, IPAddresses: []nodeTypes.Address{
		{
			Type: addressing.NodeInternalIP,
			IP:   net.ParseIP("10.0.0.1"),
		},
	}}
	mngr.NodeUpdated(n1agent)
	select {
	case nodeEvent := <-dp.NodeUpdateEvent:
		require.Equal(t, n1agent, nodeEvent)
	case nodeEvent := <-dp.NodeAddEvent:
		t.Errorf("Unexpected NodeAdd() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeUpdate() event for node1")
	}

	// kubernetes cannot overwrite local node
	mngr.NodeUpdated(n1k8s)
	select {
	case nodeEvent := <-dp.NodeAddEvent:
		t.Errorf("Unexpected NodeAdd() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(100 * time.Millisecond):
	}

	// delete from kubernetes, should not remove local node
	mngr.NodeDeleted(n1k8s)
	select {
	case nodeEvent := <-dp.NodeAddEvent:
		t.Errorf("Unexpected NodeAdd() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(100 * time.Millisecond):
	}

	mngr.NodeDeleted(n1agent)
	select {
	case nodeEvent := <-dp.NodeAddEvent:
		t.Errorf("Unexpected NodeAdd() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		require.Equal(t, n1agent, nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeDelete() event for node1")
	}
}

func BenchmarkUpdateAndDeleteCycle(b *testing.B) {
	dp := fakeTypes.NewNodeHandler()
	h, _ := cell.NewSimpleHealth()
	mngr, err := New(NodeManagerParams{
		DaemonConfig: &option.DaemonConfig{},
		IPSetMgr:     newIPSetMock(),
		NodeMetrics:  NewNodeMetrics(),
		Health:       h})
	require.NoError(b, err)
	mngr.Subscribe(dp)
	defer mngr.Stop(context.TODO())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := nodeTypes.Node{Name: fmt.Sprintf("%d", i), Source: source.Local}
		mngr.NodeUpdated(n)
	}

	for i := 0; i < b.N; i++ {
		n := nodeTypes.Node{Name: fmt.Sprintf("%d", i), Source: source.Local}
		mngr.NodeDeleted(n)
	}
	b.StopTimer()
}

func TestClusterSizeDependantInterval(t *testing.T) {
	setup(t)

	mngr := newFixture(t, NodeManagerParams{})

	prevInterval := time.Nanosecond

	for i := 0; i < 1000; i++ {
		n := nodeTypes.Node{Name: fmt.Sprintf("%d", i), Source: source.Local, IPAddresses: []nodeTypes.Address{
			{
				Type: addressing.NodeInternalIP,
				IP:   net.ParseIP("10.0.0.1"),
			},
		}}
		mngr.NodeUpdated(n)
		newInterval := mngr.ClusterSizeDependantInterval(time.Minute)
		assert.Greater(t, newInterval, prevInterval)
	}
}

/* FIXME
func TestBackgroundSync(t *testing.T) {
	signalNodeHandler := newSignalNodeHandler()
	signalNodeHandler.EnableNodeValidateImplementationEvent = true
	ipcacheMock := newIPcacheMock()
	mngr := newFixture(t, NodeManagerParams{IPCache: ipcacheMock})
	mngr.Subscribe(signalNodeHandler)

	numNodes := 128

	allNodeValidateCallsReceived := &sync.WaitGroup{}
	allNodeValidateCallsReceived.Add(1)

	go func() {
		nodeValidationsReceived := 0
		timer, timerDone := inctimer.New()
		defer timerDone()
		for {
			select {
			case <-signalNodeHandler.NodeValidateImplementationEvent:
				nodeValidationsReceived++
				if nodeValidationsReceived >= numNodes {
					allNodeValidateCallsReceived.Done()
					return
				}
			case <-timer.After(time.Second * 1):
				t.Errorf("Timeout while waiting for NodeValidateImplementation() to be called")
				allNodeValidateCallsReceived.Done()
				return
			}
		}
	}()

	for i := 0; i < numNodes; i++ {
		n := nodeTypes.Node{Name: fmt.Sprintf("%d", i), Source: source.Kubernetes, IPAddresses: []nodeTypes.Address{
			{
				Type: addressing.NodeInternalIP,
				IP:   net.ParseIP("10.0.0.1"),
			},
		}}
		mngr.NodeUpdated(n)
	}

	panic("FIXME")
	//mngr.singleBackgroundLoop(context.Background(), time.Millisecond)

	allNodeValidateCallsReceived.Wait()
}*/

/* FIXME
func TestNodeManagerEmitStatus(t *testing.T) {
	// Tests health reporting on node manager.
	assert := assert.New(t)

	baseBackgroundSyncInterval = 1 * time.Millisecond
	fn := func(m *manager, sh hive.Shutdowner, statusTable statedb.Table[types.Status], db *statedb.DB) {
		defer sh.Shutdown()
		m.nodes[nodeTypes.Identity{
			Name:    "node1",
			Cluster: "c1",
		}] = &nodeEntry{node: nodeTypes.Node{Name: "node1", Cluster: "c1"}}
		m.nodeHandlers = make(map[datapath.NodeHandler]struct{})
		nh1 := newSignalNodeHandler()
		nh1.EnableNodeValidateImplementationEvent = true
		// By default this is a buffered channel, by making it a non-buffered
		// channel we can sync up iterations of background sync.
		nh1.NodeValidateImplementationEvent = make(chan nodeTypes.Node)
		nh1.NodeValidateImplementationEventError = fmt.Errorf("test error")
		m.nodeHandlers[nh1] = struct{}{}

		done := make(chan struct{})
		reattempt := make(chan struct{})
		checkStatus := func(old statedb.Revision) (types.Status, <-chan struct{}, statedb.Revision) {
			tx := db.ReadTxn()

			id := types.Identifier{
				Module:    cell.FullModuleID{"node_manager"},
				Component: []string{"background-sync"},
			}
			for {
				ss, cur, watch, _ := statusTable.GetWatch(tx, health.PrimaryIndex.Query(id.HealthID()))
				if cur != old {
					return ss, watch, cur
				}
				<-watch
			}
		}
		go func() {
			status, watch, rev := checkStatus(99)
			assert.Equal(types.Level(""), status.Level)
			<-watch
			status, watch, rev = checkStatus(rev)
			assert.Equal(types.LevelDegraded, string(status.Level))
			close(reattempt)
			<-watch
			status, _, _ = checkStatus(rev)
			assert.Equal(types.LevelOK, string(status.Level))
		}()
		go func() {
			<-nh1.NodeValidateImplementationEvent
			<-reattempt
			nh1.NodeValidateImplementationEventError = nil
			<-nh1.NodeValidateImplementationEvent
			close(done)
		}()
		// Start the manager
		assert.NoError(m.Start(context.Background()))
		<-done
	}
	ipcacheMock := newIPcacheMock()
	config := &option.DaemonConfig{}
	err := hive.New(
		cell.Provide(func() testParams {
			return testParams{
				Config:        config,
				IPCache:       ipcacheMock,
				IPSet:         newIPSetMock(),
				NodeMetrics:   NewNodeMetrics(),
				IPSetFilterFn: func(no *nodeTypes.Node) bool { return false },
			}
		}),
		cell.Module("node_manager", "Node Manager", cell.Provide(New)),
		cell.Invoke(fn),
	).Run(hivetest.Logger(t))
	assert.NoError(err)
}

type testParams struct {
	cell.Out
	Config        *option.DaemonConfig
	IPCache       IPCache
	IPSet         ipset.Manager
	NodeMetrics   *nodeMetrics
	IPSetFilterFn IPSetFilterFn
}*/

/*
func TestNodeWithSameInternalIP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	allocator := testidentity.NewMockIdentityAllocator(nil)
	ipcache := ipcache.NewIPCache(&ipcache.Configuration{
		Context:           ctx,
		IdentityAllocator: allocator,
		PolicyHandler:     &mockUpdater{},
		DatapathHandler:   &mockTriggerer{},
	})
	defer cancel()
	dp := newSignalNodeHandler()
	dp.EnableNodeAddEvent = true
	dp.EnableNodeUpdateEvent = true
	dp.EnableNodeDeleteEvent = true
	mngr := newFixture(t, NodeManagerParams{
		DaemonConfig: &option.DaemonConfig{LocalRouterIPv4: "169.254.4.6"},
	})
	mngr.Subscribe(dp)
	defer mngr.Stop(context.TODO())

	n1 := nodeTypes.Node{
		Name:    "node1",
		Cluster: "c1",
		IPAddresses: []nodeTypes.Address{
			{
				Type: addressing.NodeInternalIP,
				IP:   net.ParseIP("10.128.0.40"),
			},
			{
				Type: addressing.NodeExternalIP,
				IP:   net.ParseIP("34.171.135.203"),
			},
			{
				Type: addressing.NodeCiliumInternalIP,
				IP:   net.ParseIP("169.254.4.6"),
			},
		},
		Source: source.Local,
	}
	mngr.NodeUpdated(n1)

	select {
	case nodeEvent := <-dp.NodeAddEvent:
		require.Equal(t, n1, nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeAdd() event for node1")
	}

	n2 := nodeTypes.Node{
		Name:    "node2",
		Cluster: "c1",
		IPAddresses: []nodeTypes.Address{
			{
				Type: addressing.NodeInternalIP,
				IP:   net.ParseIP("10.128.0.110"),
			},
			{
				Type: addressing.NodeExternalIP,
				IP:   net.ParseIP("34.170.71.139"),
			},
			{
				Type: addressing.NodeCiliumInternalIP,
				IP:   net.ParseIP("169.254.4.6"),
			},
		},
		Source: source.CustomResource,
	}
	mngr.NodeUpdated(n2)

	select {
	case nodeEvent := <-dp.NodeAddEvent:
		require.Equal(t, n2, nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeAdd() event for node1")
	}
}*/

// TestNodeIpset tests that the ipset entries on the node are updated correctly
// when a node is updated or removed.
// It is inspired from TestNode() in manager_test.go.
func TestNodeIpset(t *testing.T) {
	ipsetExpect := func(ipsetMgr *ipsetMock, ip string, expected bool) {
		setName := ipset.CiliumNodeIPSetV6
		if v4 := net.ParseIP(ip).To4(); v4 != nil {
			setName = ipset.CiliumNodeIPSetV4
		}

		found, err := ipsetContains(ipsetMgr, setName, strings.ToLower(ip))
		require.NoError(t, err)

		if found && !expected {
			t.Errorf("ipset %s contains IP %s but it should not", setName, ip)
		}
		if !found && expected {
			t.Errorf("ipset %s does not contain expected IP %s", setName, ip)
		}
	}

	dp := newSignalNodeHandler()
	dp.EnableNodeAddEvent = true
	dp.EnableNodeUpdateEvent = true
	dp.EnableNodeDeleteEvent = true
	filter := func(no *nodeTypes.Node) bool { return no.Name != "node1" }

	mngr := newFixture(t, NodeManagerParams{
		DaemonConfig: &option.DaemonConfig{
			RoutingMode:          option.RoutingModeNative,
			EnableIPv4Masquerade: true,
		},
		IPSetFilter: filter,
	})
	mngr.Subscribe(dp)

	n1 := nodeTypes.Node{
		Name:    "node1",
		Cluster: "c1",
		IPAddresses: []nodeTypes.Address{
			{
				Type: addressing.NodeCiliumInternalIP,
				IP:   net.ParseIP("192.0.2.1"),
			},
			{
				Type: addressing.NodeCiliumInternalIP,
				IP:   net.ParseIP("2001:DB8::1"),
			},
			{
				Type: addressing.NodeInternalIP,
				IP:   net.ParseIP("10.0.0.1"),
			},
			{
				Type: addressing.NodeInternalIP,
				IP:   net.ParseIP("2001:ABCD::1"),
			},
		},
		IPv4HealthIP: net.ParseIP("192.0.2.2"),
		IPv6HealthIP: net.ParseIP("2001:DB8::2"),
		Source:       source.KVStore,
	}
	mngr.NodeUpdated(n1)

	select {
	case nodeEvent := <-dp.NodeAddEvent:
		require.Equal(t, n1, nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeAdd() event")
	}

	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "192.0.2.1", false)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "2001:DB8::1", false)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "10.0.0.1", true)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "2001:ABCD::1", true)

	n2 := nodeTypes.Node{
		Name:    "node2",
		Cluster: "c1",
		IPAddresses: []nodeTypes.Address{
			{
				Type: addressing.NodeInternalIP,
				IP:   net.ParseIP("10.1.0.1"),
			},
			{
				Type: addressing.NodeInternalIP,
				IP:   net.ParseIP("2001:ABCE::1"),
			},
		},
		Source: source.CustomResource,
	}
	mngr.NodeUpdated(n2)

	select {
	case nodeEvent := <-dp.NodeAddEvent:
		require.Equal(t, n2, nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeAdd() event")
	}

	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "10.0.0.1", true)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "2001:ABCD::1", true)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "10.1.0.1", false)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "2001:ABCE::1", false)

	n1.IPv4HealthIP = net.ParseIP("192.0.2.20")
	mngr.NodeUpdated(n1)

	select {
	case nodeEvent := <-dp.NodeAddEvent:
		t.Errorf("Unexpected NodeAdd() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		require.Equal(t, n1, nodeEvent)
	case nodeEvent := <-dp.NodeDeleteEvent:
		t.Errorf("Unexpected NodeDelete() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeUpdate() event")
	}

	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "192.0.2.1", false)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "2001:DB8::1", false)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "10.0.0.1", true)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "2001:ABCD::1", true)

	mngr.NodeDeleted(n1)
	select {
	case nodeEvent := <-dp.NodeDeleteEvent:
		require.Equal(t, n1, nodeEvent)
	case nodeEvent := <-dp.NodeAddEvent:
		t.Errorf("Unexpected NodeAdd() event %#v", nodeEvent)
	case nodeEvent := <-dp.NodeUpdateEvent:
		t.Errorf("Unexpected NodeUpdate() event %#v", nodeEvent)
	case <-time.After(3 * time.Second):
		t.Errorf("timeout while waiting for NodeDelete() event")
	}

	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "192.0.2.1", false)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "2001:DB8::1", false)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "10.0.0.1", false)
	ipsetExpect(mngr.ipsetMgr.(*ipsetMock), "2001:ABCD::1", false)
}

// Tests that the node manager calls delete on nodes to be pruned.
func TestNodesStartupPruning(t *testing.T) {
	n1 := nodeTypes.Node{Name: "node1", Cluster: "c1", IPAddresses: []nodeTypes.Address{
		{
			Type: addressing.NodeInternalIP,
			IP:   net.ParseIP("10.0.0.1"),
		},
	}}

	n2 := nodeTypes.Node{Name: "node2", Cluster: "c1", IPAddresses: []nodeTypes.Address{
		{
			Type: addressing.NodeInternalIP,
			IP:   net.ParseIP("10.0.0.2"),
		},
	}}

	n3 := nodeTypes.Node{Name: "node3", Cluster: "c2", IPAddresses: []nodeTypes.Address{
		{
			Type: addressing.NodeInternalIP,
			IP:   net.ParseIP("10.0.0.3"),
		},
	}}

	// Create a nodes.json file from the above two nodes, simulating a previous instance of the agent.
	tmp := t.TempDir()
	path := filepath.Join(tmp, nodesFilename)
	nf, err := os.Create(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		nf.Close()
		os.Remove(path)
	})
	e := json.NewEncoder(nf)
	require.NoError(t, e.Encode([]nodeTypes.Node{n3, n2, n1}))
	require.NoError(t, nf.Sync())
	require.NoError(t, nf.Close())

	checkNodeFileMatches := func(path string, node nodeTypes.Node) {
		// Wait until the file exists. The node deletion triggers the write, hence
		// this shouldn't take long.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.FileExists(c, path)
		}, time.Second, 10*time.Millisecond)
		nwf, err := os.Open(path)
		require.NoError(t, err)
		t.Cleanup(func() {
			nwf.Close()
		})
		var nl []nodeTypes.Node
		assert.NoError(t, json.NewDecoder(nwf).Decode(&nl))
		assert.Len(t, nl, 1)
		assert.Equal(t, node, nl[0])
		require.NoError(t, os.Remove(path))
	}

	// Create a node manager and add only node1.
	dp := newSignalNodeHandler()
	dp.EnableNodeDeleteEvent = true
	mngr := newFixture(t, NodeManagerParams{
		DaemonConfig: &option.DaemonConfig{
			StateDir:    tmp,
			ClusterName: "c1",
		},
	})
	mngr.Subscribe(dp)
	mngr.NodeUpdated(n1)

	// Load the nodes from disk and initiate pruning. This should prune node 2
	// (since it's present in the file but not in our current view).
	mngr.restoreNodeCheckpoint()
	require.NoError(t, mngr.initNodeCheckpointer(time.Microsecond))
	// We remove our test file here to be able to tell once the nodemanager has
	// written one itself.
	require.NoError(t, os.Remove(path))
	// Declare cluster nodes synced (but not clustermesh nodes)
	mngr.NodeSync()

	select {
	case dn := <-dp.NodeDeleteEvent:
		t.Logf("delete event: %v", dn)
		n2r := n2
		n2r.Source = source.Restored
		assert.Equal(t, n2r, dn, "should have deleted node 2 and (with source=Restored)")
	case <-time.After(time.Second * 5):
		t.Fatal("should have received a node deletion event for node 2")
	}

	checkNodeFileMatches(path, n1)

	// Allow pruning the clustermesh node.
	mngr.MeshNodeSync()

	select {
	case dn := <-dp.NodeDeleteEvent:
		n3r := n3
		n3r.Source = source.Restored
		assert.Equal(t, n3r, dn, "should have deleted node 3 and (with source=Restored)")
	case <-time.After(time.Second * 5):
		t.Fatal("should have received a node deletion event for node 3")
	}

	checkNodeFileMatches(path, n1)
}
