package services

import (
	"flag"
	"os"
	"testing"

	lb "github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/testutils/mockmaps"
)

// Flags
var (
	flagUpdate = flag.Bool("update", false, "Update golden test files")
	flagDebug  = flag.Bool("debug", false, "Enable debug logging")
)

func TestMain(m *testing.M) {
	flag.Parse()
	if *flagDebug {
		logging.SetLogLevelToDebug()
	}
	logging.InitializeDefaultLogger()

	option.Config.Populate()
	option.Config.EnableHealthCheckNodePort = false

	os.Exit(m.Run())
}

func TestGracefulTermination(t *testing.T) {
	defer setOption(&option.Config.EnableK8sTerminatingEndpoint).restore()
	NewGoldenTest(t, "graceful-termination", *flagUpdate).Run(t)
}

func TestDualStack(t *testing.T) {
	defer setOption(&option.Config.EnableIPv6).restore()
	defer setOption(&option.Config.EnableNodePort).restore()
	testCase := NewGoldenTest(t, "dual-stack", *flagUpdate)

	// FIXME remove use of testing.T from within validation
	testCase.Steps[0].AddValidation(func(lbmap *mockmaps.LBMockMap) error {
		assert := lbmapAssert{t, lbmap}

		// Verify that default/echo-dualstack service exists
		// for both NodePort and ClusterIP, and that it has backends
		// for udp:69, and tcp:80 for both IPv4 and IPv6.
		assert.servicesExist(
			"default/echo-dualstack",
			[]lb.SVCType{lb.SVCTypeNodePort, lb.SVCTypeClusterIP},
			[]svcL3Type{svcIPv4, svcIPv6},
			lb.UDP,
			69)
		assert.servicesExist(
			"default/echo-dualstack",
			[]lb.SVCType{lb.SVCTypeNodePort, lb.SVCTypeClusterIP},
			[]svcL3Type{svcIPv4, svcIPv6},
			lb.TCP,
			80)

		return nil
	})

	testCase.Run(t)
}

//
// LBMap assertions
//

type svcL3Type string

const (
	svcIPv4 = svcL3Type("IPv4")
	svcIPv6 = svcL3Type("IPV6")
)

type lbmapAssert struct {
	t     *testing.T
	lbmap *mockmaps.LBMockMap
}

// findService finds a service matching the parameters and that it has a
// backend with specified l4 type and port.
//
// Linear in time, but should be OK for tests with few 10s of services
// and backends.
func (a lbmapAssert) findServiceWithBackend(name string, svcType lb.SVCType, l3 svcL3Type, l4 lb.L4Type, port uint16) *lb.SVC {
	for _, svc := range a.lbmap.ServiceByID {
		svcL3Type := svcIPv4
		if svc.Frontend.IsIPv6() {
			svcL3Type = svcIPv6
		}
		match := svc.Type == svcType
		match = match && l3 == svcL3Type
		l4match := false
		for _, be := range svc.Backends {
			if l4 == be.L4Addr.Protocol && be.L4Addr.Port == port {
				l4match = true
				break
			}
		}
		match = match && l4match
		if match {
			return svc
		}
	}
	return nil
}

// servicesExist asserts that the service with given name (<namespace>/<name>) exists for
// the listed service types and has frontends with the given L3 protocols, and that
// it has backend with the given L4 type and port.
// TODO: this does too much. break up service and backend assertions?
func (a lbmapAssert) servicesExist(name string, svcTypes []lb.SVCType, l3s []svcL3Type, l4 lb.L4Type, port uint16) {
	for _, svcType := range svcTypes {
		for _, l3 := range l3s {
			if svc := a.findServiceWithBackend(name, svcType, l3, l4, port); svc == nil {
				a.t.Fatalf("Service for name=%q, type=%q, l3=%q, l4=%q, port=%d not found", name, svcType, l3, l4, port)
			}
		}
	}
}

//
// Utils for working with option.Config.
//

type oldOption struct {
	opt *bool
	old bool
}

func setOption(opt *bool) oldOption {
	old := oldOption{opt, *opt}
	*opt = true
	return old
}

func unsetOption(opt *bool) oldOption {
	old := oldOption{opt, *opt}
	*opt = false
	return old
}

func (o oldOption) restore() {
	*o.opt = o.old
}
