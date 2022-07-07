// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

//go:build mock

package probes

import "errors"

type KernelParam string

// SystemConfig contains kernel configuration and sysctl parameters related to
// BPF functionality.
type SystemConfig struct {
	UnprivilegedBpfDisabled      int         `json:"unprivileged_bpf_disabled"`
	BpfJitEnable                 int         `json:"bpf_jit_enable"`
	BpfJitHarden                 int         `json:"bpf_jit_harden"`
	BpfJitKallsyms               int         `json:"bpf_jit_kallsyms"`
	BpfJitLimit                  int         `json:"bpf_jit_limit"`
	ConfigBpf                    KernelParam `json:"CONFIG_BPF"`
	ConfigBpfSyscall             KernelParam `json:"CONFIG_BPF_SYSCALL"`
	ConfigHaveEbpfJit            KernelParam `json:"CONFIG_HAVE_EBPF_JIT"`
	ConfigBpfJit                 KernelParam `json:"CONFIG_BPF_JIT"`
	ConfigBpfJitAlwaysOn         KernelParam `json:"CONFIG_BPF_JIT_ALWAYS_ON"`
	ConfigCgroups                KernelParam `json:"CONFIG_CGROUPS"`
	ConfigCgroupBpf              KernelParam `json:"CONFIG_CGROUP_BPF"`
	ConfigCgroupNetClassID       KernelParam `json:"CONFIG_CGROUP_NET_CLASSID"`
	ConfigSockCgroupData         KernelParam `json:"CONFIG_SOCK_CGROUP_DATA"`
	ConfigBpfEvents              KernelParam `json:"CONFIG_BPF_EVENTS"`
	ConfigKprobeEvents           KernelParam `json:"CONFIG_KPROBE_EVENTS"`
	ConfigUprobeEvents           KernelParam `json:"CONFIG_UPROBE_EVENTS"`
	ConfigTracing                KernelParam `json:"CONFIG_TRACING"`
	ConfigFtraceSyscalls         KernelParam `json:"CONFIG_FTRACE_SYSCALLS"`
	ConfigFunctionErrorInjection KernelParam `json:"CONFIG_FUNCTION_ERROR_INJECTION"`
	ConfigBpfKprobeOverride      KernelParam `json:"CONFIG_BPF_KPROBE_OVERRIDE"`
	ConfigNet                    KernelParam `json:"CONFIG_NET"`
	ConfigXdpSockets             KernelParam `json:"CONFIG_XDP_SOCKETS"`
	ConfigLwtunnelBpf            KernelParam `json:"CONFIG_LWTUNNEL_BPF"`
	ConfigNetActBpf              KernelParam `json:"CONFIG_NET_ACT_BPF"`
	ConfigNetClsBpf              KernelParam `json:"CONFIG_NET_CLS_BPF"`
	ConfigNetClsAct              KernelParam `json:"CONFIG_NET_CLS_ACT"`
	ConfigNetSchIngress          KernelParam `json:"CONFIG_NET_SCH_INGRESS"`
	ConfigXfrm                   KernelParam `json:"CONFIG_XFRM"`
	ConfigIPRouteClassID         KernelParam `json:"CONFIG_IP_ROUTE_CLASSID"`
	ConfigIPv6Seg6Bpf            KernelParam `json:"CONFIG_IPV6_SEG6_BPF"`
	ConfigBpfLircMode2           KernelParam `json:"CONFIG_BPF_LIRC_MODE2"`
	ConfigBpfStreamParser        KernelParam `json:"CONFIG_BPF_STREAM_PARSER"`
	ConfigNetfilterXtMatchBpf    KernelParam `json:"CONFIG_NETFILTER_XT_MATCH_BPF"`
	ConfigBpfilter               KernelParam `json:"CONFIG_BPFILTER"`
	ConfigBpfilterUmh            KernelParam `json:"CONFIG_BPFILTER_UMH"`
	ConfigTestBpf                KernelParam `json:"CONFIG_TEST_BPF"`
	ConfigKernelHz               KernelParam `json:"CONFIG_HZ"`
}

// MapTypes contains bools indicating which types of BPF maps the currently
// running kernel supports.
type MapTypes struct {
	HaveHashMapType                bool `json:"have_hash_map_type"`
	HaveArrayMapType               bool `json:"have_array_map_type"`
	HaveProgArrayMapType           bool `json:"have_prog_array_map_type"`
	HavePerfEventArrayMapType      bool `json:"have_perf_event_array_map_type"`
	HavePercpuHashMapType          bool `json:"have_percpu_hash_map_type"`
	HavePercpuArrayMapType         bool `json:"have_percpu_array_map_type"`
	HaveStackTraceMapType          bool `json:"have_stack_trace_map_type"`
	HaveCgroupArrayMapType         bool `json:"have_cgroup_array_map_type"`
	HaveLruHashMapType             bool `json:"have_lru_hash_map_type"`
	HaveLruPercpuHashMapType       bool `json:"have_lru_percpu_hash_map_type"`
	HaveLpmTrieMapType             bool `json:"have_lpm_trie_map_type"`
	HaveArrayOfMapsMapType         bool `json:"have_array_of_maps_map_type"`
	HaveHashOfMapsMapType          bool `json:"have_hash_of_maps_map_type"`
	HaveDevmapMapType              bool `json:"have_devmap_map_type"`
	HaveSockmapMapType             bool `json:"have_sockmap_map_type"`
	HaveCpumapMapType              bool `json:"have_cpumap_map_type"`
	HaveXskmapMapType              bool `json:"have_xskmap_map_type"`
	HaveSockhashMapType            bool `json:"have_sockhash_map_type"`
	HaveCgroupStorageMapType       bool `json:"have_cgroup_storage_map_type"`
	HaveReuseportSockarrayMapType  bool `json:"have_reuseport_sockarray_map_type"`
	HavePercpuCgroupStorageMapType bool `json:"have_percpu_cgroup_storage_map_type"`
	HaveQueueMapType               bool `json:"have_queue_map_type"`
	HaveStackMapType               bool `json:"have_stack_map_type"`
}

// Kernel support for miscellaneous BPF features.
type Misc struct {
	HaveLargeInsnLimit bool `json:"have_large_insn_limit"`
	HaveBoundedLoops   bool `json:"have_bounded_loops"`
	HaveV2ISAExtension bool `json:"have_v2_isa_extension"`
	HaveV3ISAExtension bool `json:"have_v3_isa_extension"`
}

// Features contains BPF feature checks returned by bpftool.
type Features struct {
	SystemConfig `json:"system_config"`
	MapTypes     `json:"map_types"`
	Helpers      map[string][]string `json:"helpers"`
	Misc         `json:"misc"`
}

// ProbeManager is a manager of BPF feature checks.
type ProbeManager struct {
	features Features
}

func NewProbeManager() *ProbeManager {
	f := Features{
		SystemConfig: SystemConfig{},
		Misc: Misc{
			HaveLargeInsnLimit: true,
		},
	}

	return &ProbeManager{f}
}

func (pm *ProbeManager) Probe() Features {
	return pm.features
}
func (pm *ProbeManager) Features() Features {
	return pm.features
}
func (p *ProbeManager) GetHelpers(prog string) map[string]struct{} {
	return nil
}
func (p *ProbeManager) GetMisc() Misc {
	return p.features.Misc
}
func (p *ProbeManager) GetMapTypes() *MapTypes {
	return &p.features.MapTypes
}
func (p *ProbeManager) SystemConfigProbes() error {
	return nil
}
func (p *ProbeManager) CreateHeadersFile() error {
	return nil
}

var ErrKernelConfigNotFound = errors.New("Kernel Config file not found")

func (p *ProbeManager) SystemKernelHz() (int, error) {
	return 100, nil
}
