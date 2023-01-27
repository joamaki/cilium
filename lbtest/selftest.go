package main

import (
	"fmt"
	"net"
	"time"

	"github.com/cilium/cilium/controlplane/servicemanager"
	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

var selftestCell = cell.Module(
	"self-test",
	"Simple self-test",

	cell.Config(defaultSelfTestConfig),
	cell.Invoke(registerSelftest),
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
	Loader         *Loader
	LoaderConfig   LoaderConfig
	ServiceManager servicemanager.ServiceManager
	LBMap          types.LBMap
}

func registerSelftest(p selftestParams) {
	if !p.Config.SelfTest {
		return
	}
	p.Lifecycle.Append(hive.Hook{
		OnStart: func(ctx hive.HookContext) error {
			return selftest(p)
		},
	})
}

var (
	def_l2 = &layers.Ethernet{
		SrcMAC:       []byte{1, 2, 3, 4, 5, 6},
		DstMAC:       []byte{2, 3, 4, 5, 6, 7},
		EthernetType: layers.EthernetTypeIPv4,
	}
	clientAddr  = net.ParseIP("1.2.3.4")
	backendAddr = net.ParseIP("2.3.4.5")
)

const (
	frontendPort = 1234
	backendPort  = 44444
)

func upsertServices(h servicemanager.ServiceHandle) func() {
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
		loadbalancer.ScopeExternal,
	)

	h.UpsertBackends(name,
		&loadbalancer.Backend{FEPortName: "http", NodeName: "quux", L3n4Addr: *beAddr},
	)

	h.Synchronized()

	return func() {
		h.DeleteFrontend(fe)
	}
}

func selftest(p selftestParams) error {
	frontendAddr := net.ParseIP(p.LoaderConfig.DirectRoutingIP)

	h := p.ServiceManager.NewHandle("progtest")
	defer h.Close()

	// Asynchronously upsert the frontends and backends, and defer the deletion.
	cleanupServices := upsertServices(h)
	defer cleanupServices()

	// Wait for them to appear in the BPF maps
	waitForLBMaps(p.LBMap, 2, 1)

	prog := p.Loader.XDPProg

	pkt := newPacket(clientAddr, frontendAddr, frontendPort)

	for i := 0; i < 10; i++ {
		printPacket(p.Log, "IN", pkt)
		ret, data, err := prog.Test(pkt)
		//fmt.Printf("ret=%d, err=%s\n", ret, err)
		if err != nil {
			return err
		}
		if ret != 4 { /* XDP_REDIRECT */
			//p.Log.Warnf("progtest failed: expected XDP_REDIRECT, got %d", ret)
			return fmt.Errorf("progtest failed: expected XDP_REDIRECT, got %d", ret)
		}
		printPacket(p.Log, "OUT", data)

		outPkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Lazy)
		l3 := outPkt.NetworkLayer().(*layers.IPv4)
		if !l3.DstIP.Equal(backendAddr) {
			return fmt.Errorf("progtest failed: unexpected DstIP: %s", l3.DstIP)
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
	panic("waitForLBMaps")
}

func newPacket(srcIP, dstIP net.IP, dstPort int) []byte {
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
		SrcPort: layers.TCPPort(1111),
		DstPort: layers.TCPPort(dstPort),
		SYN:     true,
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
