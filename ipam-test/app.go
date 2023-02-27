package main

import (
	"context"
	"fmt"
	"net"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/k8s/client"
)

var App = cell.Module(
	"ipam-test",
	"IPAM test app",

	client.Cell,
	k8s.SharedResourcesCell,

	IPAMCell,
)

//
// Types
//

type AllocationResult struct {
	IP net.IP
}

type IPAM interface {
	AllocateNext(owner string) (*AllocationResult, error)
}

type Allocator interface {
	hive.HookInterface

	ModeName() string
	IPAM
}

//
// IPAM "selector"
//

var IPAMCell = cell.Module(
	"ipam",
	"IPAM",

	cell.Config(defaultIPAMConfig),
	cell.Provide(selectIPAM),

	cell.Invoke(func(IPAM) {}),

	ClusterPoolCell,
	CRDCell,
)

type ipamConfig struct {
	IPAMMode string
}

func (def ipamConfig) Flags(flags *pflag.FlagSet) {
	flags.String("ipam-mode", def.IPAMMode, "IPAM Mode")
}

var defaultIPAMConfig = ipamConfig{
	IPAMMode: "cluster-pool",
}

type ipamParams struct {
	cell.In

	Config     ipamConfig
	Lifecycle  hive.Lifecycle
	Allocators []Allocator `group:"ipam-allocators"`
}

func selectIPAM(p ipamParams) (IPAM, error) {
	for _, a := range p.Allocators {
		if a != nil && a.ModeName() == p.Config.IPAMMode {
			p.Lifecycle.Append(a)
			return a, nil
		}
	}
	return nil, fmt.Errorf("Could not find allocator %s", p.Config.IPAMMode)
}

//
// ClusterPool IPAM
//

var ClusterPoolCell = cell.Module(
	"ipam-cluster-pool",
	"Cluster-pool IPAM",
	cell.Provide(
		newClusterPool,
	),
)

type clusterpoolAllocator struct {
	clusterpoolParams
}

// Start implements hive.HookInterface
func (a *clusterpoolAllocator) Start(hive.HookContext) error {
	a.Log.Info("hello")

	go func() {
		for ev := range a.LocalCiliumNode.Events(context.TODO()) {
			a.Log.Infof("event: %#v\n", ev)
		}

	}()
	return nil
}

// Stop implements hive.HookInterface
func (a *clusterpoolAllocator) Stop(hive.HookContext) error {
	return nil
}

type clusterpoolParams struct {
	cell.In

	Log             logrus.FieldLogger
	LocalCiliumNode *k8s.LocalCiliumNodeResource
}

type clusterpoolOut struct {
	cell.Out

	Allocator Allocator `group:"ipam-allocators"`
}

// AllocateNext implements Allocator
func (*clusterpoolAllocator) AllocateNext(owner string) (*AllocationResult, error) {
	panic("unimplemented")
}

// ModeName implements Allocator
func (*clusterpoolAllocator) ModeName() string {
	return "cluster-pool"
}

var _ Allocator = &clusterpoolAllocator{}
var _ hive.HookInterface = &clusterpoolAllocator{}

func newClusterPool(p clusterpoolParams) clusterpoolOut {
	a := &clusterpoolAllocator{p}

	return clusterpoolOut{Allocator: a}
}

//
// CRD IPAM
//

var CRDCell = cell.Module(
	"ipam-crd",
	"CRD IPAM",
	cell.Provide(
		newCRD,
	),
)

type crdAllocator struct {
	crdParams
}

// Start implements hive.HookInterface
func (a *crdAllocator) Start(hive.HookContext) error {
	go func() {
		for ev := range a.Nodes.Events(context.TODO()) {
			a.Log.Infof("event: %#v\n", ev)
		}

	}()
	return nil
}

// Stop implements hive.HookInterface
func (a *crdAllocator) Stop(hive.HookContext) error {
	return nil
}

type crdParams struct {
	cell.In

	Log   logrus.FieldLogger
	Nodes *k8s.LocalNodeResource
}

type crdOut struct {
	cell.Out

	Allocator Allocator `group:"ipam-allocators"`
}

// AllocateNext implements Allocator
func (*crdAllocator) AllocateNext(owner string) (*AllocationResult, error) {
	panic("unimplemented")
}

// ModeName implements Allocator
func (*crdAllocator) ModeName() string {
	return "crd"
}

var _ Allocator = &crdAllocator{}
var _ hive.HookInterface = &crdAllocator{}

func newCRD(p crdParams) crdOut {
	a := &crdAllocator{p}

	return crdOut{Allocator: a}
}
