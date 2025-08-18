package maps

import (
	"slices"
	"sync/atomic"

	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/hive/cell"
)

type FrontendBackendAddress struct {
	Frontend, Backend loadbalancer.L3n4Addr
}

// Restored data from BPF maps used by the [writer.Writer] and [reconciler.BPFOps].
type Restored struct {
	m LBMaps

	BackendHealth    lock.Map[FrontendBackendAddress, bool]
	BackendAddresses lock.Map[loadbalancer.BackendID, loadbalancer.L3n4Addr]
	ServiceIDs       lock.Map[loadbalancer.L3n4Addr, loadbalancer.ServiceID]
	MaxServiceID     atomic.Uint32
	BackendIDs       lock.Map[loadbalancer.L3n4Addr, loadbalancer.BackendID]
	MaxBackendID     atomic.Uint32
}

func NewRestored(lc cell.Lifecycle, m LBMaps) *Restored {
	r := &Restored{m: m}
	lc.Append(cell.Hook{
		OnStart: func(cell.HookContext) error { return r.reset() },
	})
	return r
}

func (r *Restored) reset() error {
	r.BackendHealth.Clear()
	r.BackendAddresses.Clear()
	r.ServiceIDs.Clear()
	r.MaxServiceID.Store(0)
	r.MaxBackendID.Store(0)
	r.BackendIDs.Clear()

	err := r.m.DumpBackend(func(key BackendKey, value BackendValue) {
		value = value.ToHost()
		addr := beValueToAddr(value)

		id := key.GetID()
		r.MaxBackendID.Store(max(r.MaxBackendID.Load(), uint32(id)))

		r.BackendAddresses.Store(id, addr)
		if addr.Protocol() == loadbalancer.ANY {
			// Migrate from 'ANY' protocol by reusing the ID.
			addr2 := loadbalancer.NewL3n4Addr(loadbalancer.TCP, addr.AddrCluster(), addr.Port(), addr.Scope())
			r.BackendIDs.Store(addr2, id)
			addr2 = loadbalancer.NewL3n4Addr(loadbalancer.UDP, addr.AddrCluster(), addr.Port(), addr.Scope())
			r.BackendIDs.Store(addr2, id)
			addr2 = loadbalancer.NewL3n4Addr(loadbalancer.SCTP, addr.AddrCluster(), addr.Port(), addr.Scope())
			r.BackendIDs.Store(addr2, id)
		} else {
			r.BackendIDs.Store(addr, id)
		}
	})
	if err != nil {
		return err
	}
	serviceSlots := map[loadbalancer.L3n4Addr][]ServiceValue{}
	err = r.m.DumpService(func(key ServiceKey, value ServiceValue) {
		key = key.ToHost()
		value = value.ToHost()
		addr := svcKeyToAddr(key)
		s := slices.Grow(serviceSlots[addr], key.GetBackendSlot()+1)
		s = s[:max(len(s), key.GetBackendSlot()+1)]
		s[key.GetBackendSlot()] = value
		serviceSlots[addr] = s
	})
	for addr, slots := range serviceSlots {
		// Restore the ID allocations from the BPF maps in order to reuse
		// them and thus avoiding traffic disruptions.
		master := slots[0]
		if master == nil {
			continue
		}

		id := loadbalancer.ServiceID(master.GetRevNat())
		r.MaxServiceID.Store(max(r.MaxServiceID.Load(), uint32(id)))

		if addr.Protocol() == loadbalancer.ANY {
			// Migrate from 'ANY' protocol by reusing the ID.
			addr2 := loadbalancer.NewL3n4Addr(loadbalancer.TCP, addr.AddrCluster(), addr.Port(), addr.Scope())
			r.ServiceIDs.Store(addr2, id)
			addr2 = loadbalancer.NewL3n4Addr(loadbalancer.UDP, addr.AddrCluster(), addr.Port(), addr.Scope())
			r.ServiceIDs.Store(addr2, id)
			addr2 = loadbalancer.NewL3n4Addr(loadbalancer.SCTP, addr.AddrCluster(), addr.Port(), addr.Scope())
			r.ServiceIDs.Store(addr2, id)
		} else {
			r.ServiceIDs.Store(addr, id)
		}

		for i, slot := range slots[1:] {
			if beAddr, found := r.BackendAddresses.Load(slot.GetBackendID()); found {
				healthy := i < master.GetCount()
				r.BackendHealth.Store(
					FrontendBackendAddress{addr, beAddr},
					healthy)
			}
		}
	}
	return nil
}

func svcKeyToAddr(svcKey ServiceKey) loadbalancer.L3n4Addr {
	feIP := svcKey.GetAddress()
	feAddrCluster := cmtypes.MustAddrClusterFromIP(feIP)
	proto := loadbalancer.NewL4TypeFromNumber(svcKey.GetProtocol())
	feL3n4Addr := loadbalancer.NewL3n4Addr(proto, feAddrCluster, svcKey.GetPort(), svcKey.GetScope())
	return feL3n4Addr
}

func beValueToAddr(beValue BackendValue) loadbalancer.L3n4Addr {
	beAddrCluster := beValue.GetAddress()
	proto := loadbalancer.NewL4TypeFromNumber(beValue.GetProtocol())
	beL3n4Addr := loadbalancer.NewL3n4Addr(proto, beAddrCluster, beValue.GetPort(), 0)
	return beL3n4Addr
}
