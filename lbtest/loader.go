package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/cilium/cilium/pkg/byteorder"
	"github.com/cilium/cilium/pkg/datapath/lb"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

// TODO: Move into datapath/something/loader ???

var loaderCell = cell.Module(
	"loader",
	"A simple BPF loader for lbtest",

	cell.Config(defLoaderConfig),
	cell.Provide(newLoader),
)

type LoaderConfig struct {
	ObjDir string

	// FIXME: Or set the device? And only require it if there's many detected devices?
	DirectRoutingIP string
}

func (def LoaderConfig) Flags(flags *pflag.FlagSet) {
	flags.String("obj-dir", def.ObjDir, "Path to BPF objects")
	flags.String("direct-routing-ip", def.DirectRoutingIP, "Sets the required direct routing IP")
}

var defLoaderConfig = LoaderConfig{
	ObjDir:          "/tmp/bpf",
	DirectRoutingIP: "",
}

type loaderParams struct {
	cell.In

	Lifecycle hive.Lifecycle
	Log       logrus.FieldLogger
	Config    LoaderConfig
	Devices   lb.Devices
}

func newLoader(p loaderParams) *Loader {
	l := &Loader{params: p, log: p.Log}
	p.Lifecycle.Append(l)
	return l
}

type Loader struct {
	params loaderParams
	log    logrus.FieldLogger
	ctx    context.Context
	cancel context.CancelFunc

	coll *ebpf.Collection

	XDPProg *ebpf.Program
	links   []link.Link
}

func (l *Loader) Start(startCtx hive.HookContext) error {
	if l.params.Config.DirectRoutingIP == "" {
		return fmt.Errorf("Direct routing IP is required, set it with --direct-routing-ip")
	}
	directRoutingIP := net.ParseIP(l.params.Config.DirectRoutingIP)
	if directRoutingIP == nil {
		return fmt.Errorf("Invalid direct routing IP %q", l.params.Config.DirectRoutingIP)
	}

	l.ctx, l.cancel = context.WithCancel(context.Background())

	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("RemoveMemlock: %w", err)
	}

	xdpPath := path.Join(l.params.Config.ObjDir, "bpf_xdp.o")
	if _, err := os.Stat(xdpPath); err != nil {
		return err
	}

	// TODO start lbmap first and then rewrite maps
	// here to the already opened ones. Right now I've just modified
	// map names in node_config.h to match and we're pinning them here. Yuck.
	spec, err := ebpf.LoadCollectionSpec(xdpPath)
	if err != nil {
		return fmt.Errorf("LoadCollectionSpec(%s): %w", xdpPath, err)
	}

	err = spec.RewriteConstants(map[string]any{
		"s_ipv4_loopback":       byteorder.NetIPv4ToHost32(net.ParseIP(defaults.LoopbackIPv4)),
		"s_ipv4_direct_routing": byteorder.NetIPv4ToHost32(directRoutingIP),
	})
	if err != nil {
		return fmt.Errorf("RewriteConstants(%s): %w", xdpPath, err)
	}

	// drain extra bytes.
	for _, m := range spec.Maps {
		if m.Extra != nil {
			io.Copy(io.Discard, m.Extra)
		}
	}

	// fix up program types
	for _, p := range spec.Programs {
		if p.Type == ebpf.UnspecifiedProgram {
			p.Type = ebpf.XDP
		}
	}

	opts := ebpf.CollectionOptions{
		Maps:            ebpf.MapOptions{PinPath: "/sys/fs/bpf/tc/globals"},
		Programs:        ebpf.ProgramOptions{},
		MapReplacements: map[string]*ebpf.Map{},
	}
	l.coll, err = ebpf.NewCollectionWithOptions(spec, opts)
	if err != nil {
		return fmt.Errorf("NewCollectionWithOptions(%s): %w", xdpPath, err)
	}

	// Fix up tail calls
	if err := loadCallsMap(spec, l.coll); err != nil {
		return err
	}

	l.XDPProg = l.coll.Programs["cil_xdp_entry"]
	if l.XDPProg == nil {
		return fmt.Errorf("Cannot find entrypoint")
	}

	l.params.Devices.Observe(
		l.ctx,
		l.reload,
		func(error) {})

	return nil
}

func (l *Loader) Stop(hive.HookContext) error {
	for _, link := range l.links {
		link.Close()
	}
	l.cancel()
	return nil
}

// TODO: Use "lb.worker" and retry on failures?
func (l *Loader) reload(devs []lb.Device) {
	// FIXME incremental
	for _, link := range l.links {
		link.Close()
	}
	l.links = nil

	for _, dev := range devs {
		name := dev.Name
		fn := "cil_xdp_entry"
		log := l.log.WithField("device", name).WithField("func", fn)

		iface, err := net.InterfaceByName(name)
		if err != nil {
			log.Errorf("Cannot find device %q: %w", name, err)
			continue
		}
		xdpLink, err := link.AttachXDP(link.XDPOptions{
			Program:   l.XDPProg,
			Interface: iface.Index,
		})
		if err != nil {
			log.Errorf("AttachXDP: %w", err)
			continue
		}
		l.links = append(l.links, xdpLink)
		log.Info("Attached")
	}
}

const (
	callsMapName = "test_cilium_calls_65535"
	callsMapID   = "2/"
)

func loadCallsMap(spec *ebpf.CollectionSpec, coll *ebpf.Collection) error {
	callMap, found := coll.Maps[callsMapName]
	if !found {
		return fmt.Errorf("calls map %s not found!", callsMapName)
	}

	for name, prog := range coll.Programs {
		if strings.HasPrefix(spec.Programs[name].SectionName, callsMapID) {
			indexStr := strings.TrimPrefix(spec.Programs[name].SectionName, callsMapID)
			index, err := strconv.Atoi(indexStr)
			if err != nil {
				return fmt.Errorf("atoi tail call index: %w", err)
			}

			index32 := uint32(index)
			err = callMap.Update(&index32, prog, ebpf.UpdateAny)
			if err != nil {
				return fmt.Errorf("update tailcall map: %w", err)
			}
		}
	}

	return nil
}
