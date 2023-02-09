package main

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/cilium/cilium/lbtest/controlplane/services"
	"github.com/cilium/cilium/lbtest/datapath/loader"
	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/vishvananda/netlink"
	"go.uber.org/multierr"
	"golang.org/x/sys/unix"
)

var selftestCell = cell.Module(
	"self-test",
	"Simple self-test",

	cell.Config(defaultSelfTestConfig),
	cell.Provide(newSelfTest),
	cell.Invoke(func(selftest) {}),
)

type selfTestConfig struct {
	SelfTest bool
}

var defaultSelfTestConfig = selfTestConfig{
	SelfTest: false,
}

func (def selfTestConfig) Flags(flags *pflag.FlagSet) {
	flags.Bool("self-test", def.SelfTest, "Run self test")
}

type selftestParams struct {
	cell.In

	Config         selfTestConfig
	Lifecycle      hive.Lifecycle
	Log            logrus.FieldLogger
	Loader         *loader.Loader
	LoaderConfig   loader.LoaderConfig
	ServiceManager services.ServiceManager
	LBMap          types.LBMap
}

type selftest struct {
	p selftestParams
}

func newSelfTest(p selftestParams) selftest {
	s := selftest{p}
	if p.Config.SelfTest {
		p.Lifecycle.Append(hive.Hook{
			OnStart: func(hive.HookContext) error {
				return s.Run()
			},
		})
	}
	return s
}

var (
	def_l2 = &layers.Ethernet{
		SrcMAC:       []byte{1, 2, 3, 4, 5, 6},
		DstMAC:       []byte{2, 3, 4, 5, 6, 7},
		EthernetType: layers.EthernetTypeIPv4,
	}
	clientAddr  = net.ParseIP("172.16.0.2")
	backendAddr = net.ParseIP("172.16.0.3")
)

const (
	clientPort   = 32875
	frontendPort = 5555
	backendPort  = 44444
)

func upsertServices(h services.ServiceHandle) func() {
	name := loadbalancer.ServiceName{
		Authority: loadbalancer.Authority("test"),
		Name:      "foo",
		Namespace: "bar",
	}

	fe := &loadbalancer.FENodePort{
		CommonFE: loadbalancer.CommonFE{
			Name: name,
		},
		L4Addr: *loadbalancer.NewL4Addr(loadbalancer.TCP, frontendPort),
		Scope:  loadbalancer.ScopeExternal,
	}
	h.UpsertFrontend(fe)

	beAddr := loadbalancer.NewL3n4Addr(
		"tcp",
		cmtypes.MustParseAddrCluster(backendAddr.String()),
		backendPort,
		loadbalancer.ScopeExternal)

	h.UpsertBackends(name,
		&loadbalancer.Backend{FEPortName: "http", NodeName: "quux", L3n4Addr: *beAddr},
	)

	h.Synchronized()

	return func() {
		h.DeleteFrontend(fe)
	}
}

func setupLink(ctx context.Context) error {
	iface := "dummy_lbst0"

	// We're running in a temporary empty network namespace. Let's add a device and some
	// routes and neighbors for fib lookup to work with.
	link, err := createLink(&netlink.Dummy{}, iface, "172.16.0.1/24")
	if err != nil {
		return fmt.Errorf("createLink: %w", err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("linkSetUp(%s): %w", iface, err)
	}

	// Add neighbours and make sure they won't be removed, e.g. by hardware address
	// change by udevd.
	updates := make(chan netlink.NeighUpdate)
	netlink.NeighSubscribe(updates, ctx.Done())

	addNeigh(link, clientAddr, 0x1)
	addNeigh(link, backendAddr, 0x2)
	go func() {
		for u := range updates {
			if u.Neigh.LinkIndex == link.Attrs().Index && u.Type == unix.RTM_DELNEIGH {
				if u.Neigh.IP.Equal(clientAddr) {
					addNeigh(link, clientAddr, 0x1)
				} else {
					addNeigh(link, backendAddr, 0x2)
				}
			}
		}
	}()
	return nil
}

func (s selftest) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup a dummy device with FIB entries.
	if err := setupLink(ctx); err != nil {
		return err
	}
	defer delLink("dummy_lbst0")

	p := s.p
	frontendAddr := net.ParseIP(p.LoaderConfig.DirectRoutingIP)

	h := p.ServiceManager.NewHandle("progtest")
	defer h.Close()

	// Asynchronously upsert the frontends and backends, and defer the deletion.
	cleanupServices := upsertServices(h)
	defer cleanupServices()

	// Wait for them to appear in the BPF maps
	p.Log.Info("Waiting for LB maps to settle")
	waitForLBMaps(p.LBMap, 2, 1)

	prog := p.Loader.XDPProg

	p.Log.Info("Starting self-test")

	for i := 0; i < 10; i++ {
		// Send the ingress packet, clientAddr:clientPort -> frontendAddr:frontendPort
		pktIngress := newPacket(false, clientAddr, frontendAddr, clientPort, frontendPort)
		printPacket(p.Log, "INGRESS-IN", pktIngress)

		ret, data, err := prog.Test(pktIngress)
		if err != nil {
			return err
		} else if ret != 4 { /* XDP_REDIRECT */
			return fmt.Errorf("progtest failed: expected XDP_REDIRECT, got %d", ret)
		}
		printPacket(p.Log, "INGRESS-OUT", data)

		ap := assertPacket(data)

		if err = ap.SrcIP(frontendAddr).
			DstIP(backendAddr).
			DstPort(backendPort).
			Error(); err != nil {
			return fmt.Errorf("self-test ingress assertion failed: %w", err)
		}

		// Construct the reply packet from backend and verify rev nat
		// backendAddr:backendPort -> frontendAddr:(snat port)
		pktEgress := newPacket(true, backendAddr, ap.l3.SrcIP, backendPort, int(ap.l4.SrcPort))
		printPacket(p.Log, "EGRESS-IN", pktEgress)

		ret, data, err = prog.Test(pktEgress)
		printPacket(p.Log, "EGRESS-OUT", data)

		if err != nil {
			return err
		} else if ret != 4 { /* XDP_REDIRECT */
			return fmt.Errorf("progtest failed: expected XDP_REDIRECT, got %d", ret)
		}

		ap = assertPacket(data)

		if err = ap.SrcIP(frontendAddr).
			DstIP(clientAddr).
			SrcPort(frontendPort).
			DstPort(clientPort).
			Error(); err != nil {
			return fmt.Errorf("self-test egress assertion failed: %w", err)
		}

		//fmt.Printf("%d/10 OK\n", i)
		time.Sleep(time.Millisecond * 10)
	}

	p.Log.Info("SELF-TEST OK")

	return nil
}

//
// Helpers
//

type packetAssertion struct {
	pkt gopacket.Packet
	l3  *layers.IPv4
	l4  *layers.TCP
	err error
}

func assertPacket(data []byte) *packetAssertion {
	a := &packetAssertion{}
	a.pkt = gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Lazy)
	// FIXME error handling, IPv6
	a.l3 = a.pkt.NetworkLayer().(*layers.IPv4)
	a.l4 = a.pkt.TransportLayer().(*layers.TCP)
	return a
}

func (a *packetAssertion) Error() error {
	return a.err
}

func (a *packetAssertion) SrcIP(expected net.IP) *packetAssertion {
	if !a.l3.SrcIP.Equal(expected) {
		a.err = multierr.Append(a.err, fmt.Errorf("SrcIP: expected %q, got %q", expected, a.l3.SrcIP))
	}
	return a
}

func (a *packetAssertion) SrcPort(expected int) *packetAssertion {
	if a.l4.SrcPort != layers.TCPPort(expected) {
		a.err = multierr.Append(a.err, fmt.Errorf("SrcPort: expected %q, got %q", expected, a.l4.SrcPort))
	}
	return a
}

func (a *packetAssertion) DstIP(expected net.IP) *packetAssertion {
	if !a.l3.DstIP.Equal(expected) {
		a.err = multierr.Append(a.err, fmt.Errorf("DstIP: expected %q, got %q", expected, a.l3.DstIP))
	}
	return a
}

func (a *packetAssertion) DstPort(expected int) *packetAssertion {
	if a.l4.DstPort != layers.TCPPort(expected) {
		a.err = multierr.Append(a.err, fmt.Errorf("DstPort: expected %q, got %q", expected, a.l4.DstPort))
	}
	return a
}

func waitForLBMaps(m types.LBMap, minServices, minBackends int) {
	for i := 0; i < 10; i++ {
		time.Sleep(100 * time.Millisecond)
		svcs, errs := m.DumpServiceMaps()
		if len(errs) != 0 {
			panic(errs[0])
		}
		if len(svcs) < minServices {
			continue
		}
		bs, err := m.DumpBackendMaps()
		if err != nil {
			panic(err)
		}
		if len(bs) < minBackends {
			continue
		}
		return
	}
	//panic("waitForLBMaps")
}

func newPacket(synAck bool, srcIP, dstIP net.IP, srcPort, dstPort int) []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	l3 := &layers.IPv4{
		TTL:      255,
		Version:  4,
		IHL:      5,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}
	l4 := &layers.TCP{
		SrcPort: layers.TCPPort(srcPort),
		DstPort: layers.TCPPort(dstPort),
		SYN:     true,
		ACK:     synAck,
	}
	l4.SetNetworkLayerForChecksum(l3)

	err := gopacket.SerializeLayers(buf, opts,
		def_l2, l3, l4,
		gopacket.Payload([]byte("hello")))
	if err != nil {
		panic(err)
	}

	return buf.Bytes()
}

func printPacket(log logrus.FieldLogger, desc string, data []byte) {
	pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Lazy)
	l3 := pkt.NetworkLayer().(*layers.IPv4)
	l4 := pkt.TransportLayer().(*layers.TCP)

	log.WithField("saddr", l3.SrcIP).
		WithField("daddr", l3.DstIP).
		WithField("sport", l4.SrcPort).
		WithField("dport", l4.DstPort).
		Debug(desc)
}

func createLink(linkTemplate netlink.Link, iface, ipAddr string) (netlink.Link, error) {
	var flags net.Flags
	*linkTemplate.Attrs() = netlink.LinkAttrs{
		Name:  iface,
		Flags: flags,
	}

	if err := netlink.LinkAdd(linkTemplate); err != nil {
		return nil, err
	}

	link, err := netlink.LinkByName(iface)
	if err != nil {
		return nil, err
	}

	if ipAddr != "" {
		if err := addAddr(link, ipAddr); err != nil {
			return nil, err
		}
	}

	return link, nil
}

func delLink(iface string) {
	link, err := netlink.LinkByName(iface)
	if err != nil {
		panic(err)
	}
	if err := netlink.LinkDel(link); err != nil {
		panic(err)
	}
}

func addAddr(link netlink.Link, cidr string) error {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	ipnet.IP = ip

	if err := netlink.AddrAdd(link, &netlink.Addr{IPNet: ipnet}); err != nil {
		return err
	}
	return nil
}

func addNeigh(link netlink.Link, ip net.IP, last byte) error {
	attrs := link.Attrs()
	neigh := &netlink.Neigh{
		LinkIndex:    attrs.Index,
		Family:       netlink.FAMILY_V4,
		State:        netlink.NUD_PERMANENT,
		IP:           ip,
		HardwareAddr: []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, last},
	}
	return netlink.NeighAdd(neigh)
}

type addRouteParams struct {
	iface string
	gw    string
	src   string
	dst   string
	table int
	scope netlink.Scope
}

func addRoute(p addRouteParams) error {
	link, err := netlink.LinkByName(p.iface)
	if err != nil {
		return err
	}

	var dst *net.IPNet
	if p.dst != "" {
		_, dst, err = net.ParseCIDR(p.dst)
		if err != nil {
			return err
		}
	}

	var src net.IP
	if p.src != "" {
		src = net.ParseIP(p.src)
	}

	if p.table == 0 {
		p.table = unix.RT_TABLE_MAIN
	}

	route := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       dst,
		Src:       src,
		Gw:        net.ParseIP(p.gw),
		Table:     p.table,
		Scope:     p.scope,
	}
	if err := netlink.RouteAdd(route); err != nil {
		return err
	}

	return nil
}
