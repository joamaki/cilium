// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package ipam

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cilium/ipam/service/ipallocator"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"go.uber.org/multierr"
	"golang.org/x/sys/unix"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cilium/cilium/pkg/cidr"
	"github.com/cilium/cilium/pkg/controller"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/inctimer"
	"github.com/cilium/cilium/pkg/ipam/types"
	"github.com/cilium/cilium/pkg/k8s"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/watchers/subscriber"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/trigger"
)

const (
	clusterPoolStatusControllerName = "sync-clusterpool-status"
	clusterPoolStatusTriggerName    = "sync-clusterpool-status-trigger"
)

// A podCIDRPool manages the allocation of IPs in multiple pod CIDRs.
// It maintains one IP allocator for each pod CIDR in the pool.
// Unused pod CIDRs which have been marked as released, but not yet deleted
// from the local CiliumNode CRD by the operator are put into the released set.
// Once the operator removes a released pod CIDR from the CiliumNode CRD spec,
// it is also deleted from the release set.
// Pod CIDRs which have been erroneously deleted from the CiliumNode CRD spec
// (either by a buggy operator or by manual/human changes CRD) are marked in
// the removed map. If IP addresses have been allocated from such a pod CIDR,
// its allocator is kept around. But no new IPs will be allocated from this
// pod CIDR. By keeping removed CIDRs in the CiliumNode CRD status, we indicate
// to the operator that we would like to re-gain ownership over that pod CIDR.
type podCIDRPool struct {
	mutex               lock.Mutex
	ipAllocators        []*ipallocator.Range
	released            map[string]struct{}
	removed             map[string]struct{}
	allocationThreshold int
	releaseThreshold    int
}

// newPodCIDRPool creates a new pod CIDR pool with the parameters used
// to manage the pod CIDR status:
//   - allocationThreshold defines the minimum number of free IPs in this pool
//     before all used CIDRs are marked as depleted (causing the operator to
//     allocate a new one)
//   - releaseThreshold defines the maximum number of free IPs in this pool
//     before unused CIDRs are marked for release.
//   - previouslyReleasedCIDRs contains a list of pod CIDRs which were allocated
//     to this node, but have been released before the agent was restarted. We
//     keep track of them to avoid accidental use-after-free after an agent restart.
func newPodCIDRPool(allocationThreshold, releaseThreshold int, previouslyReleasedCIDRs []string) *podCIDRPool {
	if allocationThreshold <= 0 {
		allocationThreshold = defaults.IPAMPodCIDRAllocationThreshold
	}

	if releaseThreshold <= 0 {
		releaseThreshold = defaults.IPAMPodCIDRReleaseThreshold
	}

	released := make(map[string]struct{}, len(previouslyReleasedCIDRs))
	for _, releasedCIDR := range previouslyReleasedCIDRs {
		released[releasedCIDR] = struct{}{}
	}

	return &podCIDRPool{
		released:            released,
		removed:             map[string]struct{}{},
		allocationThreshold: allocationThreshold,
		releaseThreshold:    releaseThreshold,
	}
}

func (p *podCIDRPool) allocate(ip net.IP) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for _, ipAllocator := range p.ipAllocators {
		cidrNet := ipAllocator.CIDR()
		if cidrNet.Contains(ip) {
			return ipAllocator.Allocate(ip)
		}
	}

	return fmt.Errorf("IP %s not in range of any pod CIDR", ip)
}

func (p *podCIDRPool) allocateNext() (net.IP, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	// When allocating a random IP, we try the pod CIDRs in the order they are
	// listed in the CRD. This avoids internal fragmentation.
	for _, ipAllocator := range p.ipAllocators {
		cidrNet := ipAllocator.CIDR()
		cidrStr := cidrNet.String()
		if _, removed := p.removed[cidrStr]; removed {
			continue
		}
		if ipAllocator.Free() == 0 {
			continue
		}
		return ipAllocator.AllocateNext()
	}

	return nil, errors.New("all pod CIDR ranges are exhausted")
}

func (p *podCIDRPool) release(ip net.IP) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for _, ipAllocator := range p.ipAllocators {
		cidrNet := ipAllocator.CIDR()
		if cidrNet.Contains(ip) {
			return ipAllocator.Release(ip)
		}
	}

	return nil
}

func (p *podCIDRPool) hasAvailableIPs() bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for _, ipAllocator := range p.ipAllocators {
		cidrNet := ipAllocator.CIDR()
		cidrStr := cidrNet.String()
		if _, removed := p.removed[cidrStr]; removed {
			continue
		}
		if ipAllocator.Free() > 0 {
			return true
		}
	}

	return false
}

func (p *podCIDRPool) inUsePodCIDRsLocked() []string {
	podCIDRs := make([]string, 0, len(p.ipAllocators))
	for _, ipAllocator := range p.ipAllocators {
		ipnet := ipAllocator.CIDR()
		podCIDRs = append(podCIDRs, ipnet.String())
	}
	return podCIDRs
}

func (p *podCIDRPool) dump() (ipToOwner map[string]string, usedIPs, freeIPs, numPodCIDRs int, err error) {
	// TODO(gandro): Use the Snapshot interface to avoid locking during dump
	p.mutex.Lock()
	defer p.mutex.Unlock()

	ipToOwner = map[string]string{}
	for _, ipAllocator := range p.ipAllocators {
		cidrNet := ipAllocator.CIDR()
		cidrStr := cidrNet.String()
		usedIPs += ipAllocator.Used()
		if _, removed := p.removed[cidrStr]; !removed {
			freeIPs += ipAllocator.Free()
		}
		ipAllocator.ForEach(func(ip net.IP) {
			ipToOwner[ip.String()] = ""
		})
	}
	numPodCIDRs = len(p.ipAllocators)

	return
}

func (p *podCIDRPool) status() types.PodCIDRMap {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	result := types.PodCIDRMap{}

	// Mark all released pod CIDRs as released.
	for cidrStr := range p.released {
		result[cidrStr] = types.PodCIDRMapEntry{
			Status: types.PodCIDRStatusReleased,
		}
	}

	// Compute the total number of free and used IPs for all non-released pod
	// CIDRs.
	totalUsed := 0
	totalFree := 0
	for _, r := range p.ipAllocators {
		cidrNet := r.CIDR()
		cidrStr := cidrNet.String()
		if _, released := p.released[cidrStr]; released {
			continue
		}
		totalUsed += r.Used()
		if _, removed := p.removed[cidrStr]; !removed {
			totalFree += r.Free()
		}
	}

	if totalFree < p.allocationThreshold {
		// If the total number of free IPs is below the allocation threshold,
		// then mark all pod CIDRs as depleted, unless they have already been
		// released.
		for _, ipAllocator := range p.ipAllocators {
			cidrNet := ipAllocator.CIDR()
			cidrStr := cidrNet.String()
			if _, released := p.released[cidrStr]; released {
				continue
			}
			result[cidrStr] = types.PodCIDRMapEntry{
				Status: types.PodCIDRStatusDepleted,
			}
		}
	} else {
		// Iterate over pod CIDRs in reverse order so we prioritize releasing
		// later pod CIDRs.
		for i := len(p.ipAllocators) - 1; i >= 0; i-- {
			ipAllocator := p.ipAllocators[i]
			cidrNet := ipAllocator.CIDR()
			cidrStr := cidrNet.String()
			if _, released := p.released[cidrStr]; released {
				continue
			}
			var status types.PodCIDRStatus
			if ipAllocator.Used() > 0 {
				// If a pod CIDR is used, then mark it as in-use or depleted.
				if ipAllocator.Free() == 0 {
					status = types.PodCIDRStatusDepleted
				} else {
					status = types.PodCIDRStatusInUse
				}
			} else if _, removed := p.removed[cidrStr]; removed {
				// Otherwise, if the pod CIDR has been removed, then mark it as released.
				p.released[cidrStr] = struct{}{}
				delete(p.removed, cidrStr)
				status = types.PodCIDRStatusReleased
				log.WithField(logfields.CIDR, cidrStr).Debug("releasing removed pod CIDR")
			} else if free := ipAllocator.Free(); totalFree-free >= p.releaseThreshold {
				// Otherwise, if the pod CIDR is not used and releasing it would
				// not take us below the release threshold, then release it and
				// mark it as released.
				p.released[cidrStr] = struct{}{}
				totalFree -= free
				status = types.PodCIDRStatusReleased
				log.WithField(logfields.CIDR, cidrStr).Debug("releasing pod CIDR")
			} else {
				// Otherwise, mark the pod CIDR as in-use.
				status = types.PodCIDRStatusInUse
			}
			result[cidrStr] = types.PodCIDRMapEntry{
				Status: status,
			}
		}
	}

	return result
}

func (p *podCIDRPool) updatePool(podCIDRs []string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if option.Config.Debug {
		log.WithFields(logrus.Fields{
			logfields.NewCIDR: podCIDRs,
			logfields.OldCIDR: p.inUsePodCIDRsLocked(),
		}).Debug("Updating IPAM pool")
	}

	// Parse the pod CIDRs, ignoring invalid CIDRs, and de-duplicating them.
	cidrNets := make([]*net.IPNet, 0, len(podCIDRs))
	cidrStrSet := make(map[string]struct{}, len(podCIDRs))
	for _, podCIDR := range podCIDRs {
		_, cidr, err := net.ParseCIDR(podCIDR)
		if err != nil {
			log.WithError(err).WithField(logfields.CIDR, podCIDR).Error("ignoring invalid pod CIDR")
			continue
		}
		if _, ok := cidrStrSet[cidr.String()]; ok {
			log.WithField(logfields.CIDR, podCIDR).Error("ignoring duplicate pod CIDR")
			continue
		}
		cidrNets = append(cidrNets, cidr)
		cidrStrSet[cidr.String()] = struct{}{}
	}

	// Forget any released pod CIDRs no longer present in the CRD.
	for cidrStr := range p.released {
		if _, ok := cidrStrSet[cidrStr]; !ok {
			log.WithField(logfields.CIDR, cidrStr).Debug("removing released pod CIDR")
			delete(p.released, cidrStr)
		}

		if option.Config.EnableUnreachableRoutes {
			if err := cleanupUnreachableRoutes(cidrStr); err != nil {
				log.WithFields(logrus.Fields{
					logfields.CIDR:  cidrStr,
					logrus.ErrorKey: err,
				}).Warning("failed to remove unreachable routes for pod cidr")
			}
		}
	}

	// newIPAllocators is the new slice of IP allocators.
	newIPAllocators := make([]*ipallocator.Range, 0, len(podCIDRs))

	// addedCIDRs is the set of pod CIDRs that have been added to newIPAllocators.
	addedCIDRs := make(map[string]struct{}, len(p.ipAllocators))

	// Add existing IP allocators to newIPAllocators in order.
	for _, ipAllocator := range p.ipAllocators {
		cidrNet := ipAllocator.CIDR()
		cidrStr := cidrNet.String()
		if _, ok := cidrStrSet[cidrStr]; !ok {
			if ipAllocator.Used() == 0 {
				continue
			}
			log.WithField(logfields.CIDR, cidrStr).Error("in-use pod CIDR was removed from spec")
			p.removed[cidrStr] = struct{}{}
		}
		newIPAllocators = append(newIPAllocators, ipAllocator)
		addedCIDRs[cidrStr] = struct{}{}
	}

	// Create and add new IP allocators to newIPAllocators.
	for _, cidrNet := range cidrNets {
		cidrStr := cidrNet.String()
		if _, ok := addedCIDRs[cidrStr]; ok {
			continue
		}
		ipAllocator, err := ipallocator.NewCIDRRange(cidrNet)
		if err != nil {
			log.WithError(err).WithField(logfields.CIDR, cidrStr).Error("cannot create *ipallocator.Range")
			continue
		}
		if ipAllocator.Free() == 0 {
			log.WithField(logfields.CIDR, cidrNet.String()).Error("skipping too-small pod CIDR")
			p.released[cidrNet.String()] = struct{}{}
			continue
		}
		log.WithField(logfields.CIDR, cidrStr).Debug("created new pod CIDR allocator")
		newIPAllocators = append(newIPAllocators, ipAllocator)
		addedCIDRs[cidrStr] = struct{}{} // Protect against duplicate CIDRs.
	}

	if len(p.ipAllocators) > 0 && len(newIPAllocators) == 0 {
		log.Warning("Removed last pod CIDR allocator")
	}

	p.ipAllocators = newIPAllocators
}

// containsCIDR checks if the outer IPNet contains the inner IPNet
func containsCIDR(outer, inner *net.IPNet) bool {
	outerMask, _ := outer.Mask.Size()
	innerMask, _ := inner.Mask.Size()
	return outerMask <= innerMask && outer.Contains(inner.IP)
}

// cleanupUnreachableRoutes remove all unreachable routes for the given pod CIDR.
// This is only needed if EnableUnreachableRoutes has been set.
func cleanupUnreachableRoutes(podCIDR string) error {
	_, removedCIDR, err := net.ParseCIDR(podCIDR)
	if err != nil {
		return err
	}

	var family int
	switch podCIDRFamily(podCIDR) {
	case IPv4:
		family = netlink.FAMILY_V4
	case IPv6:
		family = netlink.FAMILY_V6
	default:
		return errors.New("unknown pod cidr family")
	}

	routes, err := netlink.RouteListFiltered(family, &netlink.Route{
		Table: unix.RT_TABLE_MAIN,
		Type:  unix.RTN_UNREACHABLE,
	}, netlink.RT_FILTER_TABLE|netlink.RT_FILTER_TYPE)
	if err != nil {
		return fmt.Errorf("failed to fetch unreachable routes: %w", err)
	}

	var deleteErr error
	for _, route := range routes {
		if !containsCIDR(removedCIDR, route.Dst) {
			continue
		}

		err = netlink.RouteDel(&route)
		if err != nil && !errors.Is(err, unix.ESRCH) {
			// We ignore ESRCH, as it means the entry was already deleted
			err = fmt.Errorf("failed to delete unreachable route for %s: %w", route.Dst.String(), err)
			deleteErr = multierr.Append(deleteErr, err)
		}
	}

	return deleteErr
}

func podCIDRFamily(podCIDR string) Family {
	if strings.Contains(podCIDR, ":") {
		return IPv6
	}
	return IPv4
}

type nodeUpdater interface {
	UpdateStatus(ctx context.Context, ciliumNode *ciliumv2.CiliumNode, opts metav1.UpdateOptions) (*ciliumv2.CiliumNode, error)
}

type nodeWatcher interface {
	RegisterCiliumNodeSubscriber(s subscriber.CiliumNode)
}

type crdWatcher struct {
	mutex *lock.Mutex
	conf  Configuration
	owner Owner

	ipv4Pool        *podCIDRPool
	ipv6Pool        *podCIDRPool
	ipv4PoolUpdated *sync.Cond
	ipv6PoolUpdated *sync.Cond

	node *ciliumv2.CiliumNode

	controller  *controller.Manager
	k8sUpdater  *trigger.Trigger
	nodeUpdater nodeUpdater

	finishedRestore bool
}

var crdWatcherInit sync.Once
var sharedCRDWatcher *crdWatcher

func newCRDWatcher(conf Configuration, nodeWatcher nodeWatcher, owner Owner, nodeUpdater nodeUpdater) *crdWatcher {
	k8sController := controller.NewManager()
	k8sUpdater, err := trigger.NewTrigger(trigger.Parameters{
		MinInterval: 15 * time.Second,
		TriggerFunc: func(reasons []string) {
			// this is a no-op before controller is instantiated in restoreFinished
			k8sController.TriggerController(clusterPoolStatusControllerName)
		},
		Name: clusterPoolStatusTriggerName,
	})
	if err != nil {
		log.WithError(err).Fatal("Unable to initialize CiliumNode synchronization trigger")
	}

	mutex := &lock.Mutex{}
	c := &crdWatcher{
		mutex:           mutex,
		owner:           owner,
		conf:            conf,
		ipv4Pool:        nil,
		ipv6Pool:        nil,
		ipv4PoolUpdated: sync.NewCond(mutex),
		ipv6PoolUpdated: sync.NewCond(mutex),
		node:            nil,
		controller:      k8sController,
		k8sUpdater:      k8sUpdater,
		nodeUpdater:     nodeUpdater,
		finishedRestore: false,
	}

	// Subscribe to CiliumNode updates
	nodeWatcher.RegisterCiliumNodeSubscriber(c)

	return c
}

func (c *crdWatcher) localNodeUpdated(newNode *ciliumv2.CiliumNode) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// initialize pod CIDR pools from existing or new CiliumNode CRD
	if c.node == nil {
		var releasedIPv4PodCIDRs, releasedIPv6PodCIDRs []string
		for podCIDR, s := range newNode.Status.IPAM.PodCIDRs {
			if s.Status == types.PodCIDRStatusReleased {
				switch podCIDRFamily(podCIDR) {
				case IPv4:
					releasedIPv4PodCIDRs = append(releasedIPv4PodCIDRs, podCIDR)
				case IPv6:
					releasedIPv6PodCIDRs = append(releasedIPv6PodCIDRs, podCIDR)
				}
			}
		}
		allocationThreshold := newNode.Spec.IPAM.PodCIDRAllocationThreshold
		releaseThreshold := newNode.Spec.IPAM.PodCIDRReleaseThreshold

		if c.conf.IPv4Enabled() {
			c.ipv4Pool = newPodCIDRPool(allocationThreshold, releaseThreshold, releasedIPv4PodCIDRs)
		}
		if c.conf.IPv6Enabled() {
			c.ipv6Pool = newPodCIDRPool(allocationThreshold, releaseThreshold, releasedIPv6PodCIDRs)
		}
	}

	// updatePool requires that the order of pod CIDRs is maintained
	var ipv4PodCIDRs, ipv6PodCIDRs []string
	for _, podCIDR := range newNode.Spec.IPAM.PodCIDRs {
		switch podCIDRFamily(podCIDR) {
		case IPv4:
			ipv4PodCIDRs = append(ipv4PodCIDRs, podCIDR)
		case IPv6:
			ipv6PodCIDRs = append(ipv6PodCIDRs, podCIDR)
		}
	}

	if c.conf.IPv4Enabled() {
		c.ipv4Pool.updatePool(ipv4PodCIDRs)
		c.ipv4PoolUpdated.Broadcast()
	}
	if c.conf.IPv6Enabled() {
		c.ipv6Pool.updatePool(ipv6PodCIDRs)
		c.ipv6PoolUpdated.Broadcast()
	}

	// TODO(gandro): Move this parsing into updatePool
	var (
		ipv4AllocCIDRs = make([]*cidr.CIDR, 0, len(ipv4PodCIDRs))
		ipv6AllocCIDRs = make([]*cidr.CIDR, 0, len(ipv6PodCIDRs))
	)
	if c.conf.IPv4Enabled() {
		for _, podCIDR := range ipv4PodCIDRs {
			if allocCIDR, err := cidr.ParseCIDR(podCIDR); err == nil {
				ipv4AllocCIDRs = append(ipv4AllocCIDRs, allocCIDR)
			}
		}
	}
	if c.conf.IPv6Enabled() {
		for _, podCIDR := range ipv6PodCIDRs {
			if allocCIDR, err := cidr.ParseCIDR(podCIDR); err == nil {
				ipv6AllocCIDRs = append(ipv6AllocCIDRs, allocCIDR)
			}
		}
	}

	// This updates the local node routes
	c.owner.LocalAllocCIDRsUpdated(ipv4AllocCIDRs, ipv6AllocCIDRs)

	c.node = newNode
}

func (c *crdWatcher) OnAddCiliumNode(node *ciliumv2.CiliumNode, swg *lock.StoppableWaitGroup) error {
	if k8s.IsLocalCiliumNode(node) {
		c.localNodeUpdated(node)
	}

	return nil
}

func (c *crdWatcher) OnUpdateCiliumNode(oldNode, newNode *ciliumv2.CiliumNode, swg *lock.StoppableWaitGroup) error {
	if k8s.IsLocalCiliumNode(newNode) {
		c.localNodeUpdated(newNode)
	}

	return nil
}

func (c *crdWatcher) OnDeleteCiliumNode(node *ciliumv2.CiliumNode, swg *lock.StoppableWaitGroup) error {
	if k8s.IsLocalCiliumNode(node) {
		log.WithField(logfields.Node, node).Warning("Local CiliumNode deleted. IPAM will continue on last seen version")
	}

	return nil
}

func (c *crdWatcher) updateCiliumNodeStatus(ctx context.Context) error {
	var ipv4Pool, ipv6Pool *podCIDRPool
	c.mutex.Lock()
	node := c.node.DeepCopy()
	ipv4Pool = c.ipv4Pool
	ipv6Pool = c.ipv6Pool
	c.mutex.Unlock()

	if node == nil {
		return nil // waiting on localNodeUpdated to be invoked first
	}

	oldStatus := node.Status.IPAM.DeepCopy()
	node.Status.IPAM.PodCIDRs = types.PodCIDRMap{}
	if ipv4Pool != nil {
		for podCIDR, status := range c.ipv4Pool.status() {
			node.Status.IPAM.PodCIDRs[podCIDR] = status
		}
	}
	if ipv6Pool != nil {
		for podCIDR, status := range c.ipv6Pool.status() {
			node.Status.IPAM.PodCIDRs[podCIDR] = status
		}
	}

	if oldStatus.DeepEqual(&node.Status.IPAM) {
		return nil // no need to update
	}

	_, err := c.nodeUpdater.UpdateStatus(ctx, node, metav1.UpdateOptions{})
	return err
}

// restoreFinished must be called once all endpoints have been restored. This
// ensures that all previously allocated IPs have now been re-allocated and
// therefore the CIRD status can be synced with upstream. If we synced with
// upstream before we finished restoration, we would prematurely release CIDRs.
func (c *crdWatcher) restoreFinished() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.finishedRestore {
		return
	}

	// creating a new controller will execute DoFunc immediately
	c.controller.UpdateController(clusterPoolStatusControllerName, controller.ControllerParams{
		DoFunc: c.updateCiliumNodeStatus,
	})
	c.finishedRestore = true
}

func (c *crdWatcher) triggerWithReason(reason string) {
	c.k8sUpdater.TriggerWithReason(reason)
}

func (c *crdWatcher) waitForPool(family Family) <-chan *podCIDRPool {
	ch := make(chan *podCIDRPool)
	go func() {
		var pool *podCIDRPool
		c.mutex.Lock()
		switch family {
		case IPv4:
			if c.conf.IPv4Enabled() {
				for c.ipv4Pool == nil || !c.ipv4Pool.hasAvailableIPs() {
					c.ipv4PoolUpdated.Wait()
				}
				pool = c.ipv4Pool
			}
		case IPv6:
			if c.conf.IPv6Enabled() {
				for c.ipv6Pool == nil || !c.ipv6Pool.hasAvailableIPs() {
					c.ipv6PoolUpdated.Wait()
				}
				pool = c.ipv6Pool
			}
		}
		c.mutex.Unlock()
		ch <- pool
	}()
	return ch
}

type clusterPoolAllocator struct {
	pool *podCIDRPool
}

func newClusterPoolAllocator(family Family, conf Configuration, owner Owner, k8sEventReg K8sEventRegister) Allocator {
	crdWatcherInit.Do(func() {
		nodeClient := k8s.CiliumClient().CiliumV2().CiliumNodes()
		sharedCRDWatcher = newCRDWatcher(conf, k8sEventReg, owner, nodeClient)
	})

	var pool *podCIDRPool
	timer, stop := inctimer.New()
	defer stop()
	for pool == nil {
		select {
		case pool = <-sharedCRDWatcher.waitForPool(family):
			if pool == nil {
				log.WithField(logfields.Family, family).Fatal("failed to obtain pod CIDR pool for family")
			}
		case <-timer.After(5 * time.Second):
			log.WithFields(logrus.Fields{
				logfields.HelpMessage: "Check if cilium-operator pod is running and does not have any warnings or error messages.",
				logfields.Family:      family,
			}).Info("Waiting for pod CIDR pool to become available")
		}
	}

	return &clusterPoolAllocator{
		pool: pool,
	}
}

func (c *clusterPoolAllocator) Allocate(ip net.IP, owner string) (*AllocationResult, error) {
	defer sharedCRDWatcher.triggerWithReason("allocation of IP")
	return c.AllocateWithoutSyncUpstream(ip, owner)
}

func (c *clusterPoolAllocator) AllocateWithoutSyncUpstream(ip net.IP, owner string) (*AllocationResult, error) {
	if err := c.pool.allocate(ip); err != nil {
		return nil, err
	}

	return &AllocationResult{IP: ip}, nil
}

func (c *clusterPoolAllocator) AllocateNext(owner string) (*AllocationResult, error) {
	defer sharedCRDWatcher.triggerWithReason("allocation of next IP")
	return c.AllocateNextWithoutSyncUpstream(owner)
}

func (c *clusterPoolAllocator) AllocateNextWithoutSyncUpstream(owner string) (*AllocationResult, error) {
	ip, err := c.pool.allocateNext()
	if err != nil {
		return nil, err
	}

	return &AllocationResult{IP: ip}, nil
}

func (c *clusterPoolAllocator) Release(ip net.IP) error {
	defer sharedCRDWatcher.triggerWithReason("release of IP")
	return c.pool.release(ip)
}

func (c *clusterPoolAllocator) Dump() (map[string]string, string) {
	ipToOwner, usedIPs, availableIPs, numPodCIDRs, err := c.pool.dump()
	if err != nil {
		return nil, fmt.Sprintf("error: %s", err)
	}

	return ipToOwner, fmt.Sprintf("%d/%d allocated from %d pod CIDRs", usedIPs, availableIPs, numPodCIDRs)
}

func (c *clusterPoolAllocator) RestoreFinished() {
	sharedCRDWatcher.restoreFinished()
}
