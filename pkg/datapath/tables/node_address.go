package tables

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/cilium/cilium/pkg/cidr"
	"github.com/cilium/cilium/pkg/defaults"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/hive/job"
	"github.com/cilium/cilium/pkg/ip"
	"github.com/cilium/cilium/pkg/rate"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/index"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
)

type NodeAddress struct {
	Addr       netip.Addr
	DeviceName string
}

func (n *NodeAddress) IP() net.IP {
	return n.Addr.AsSlice()
}

func (n *NodeAddress) String() string {
	return fmt.Sprintf("%s (%s)", n.Addr, n.DeviceName)
}

var (
	NodeAddressTableName statedb.TableName = "node-addresses"

	NodeAddressIndex = statedb.Index[NodeAddress, netip.Addr]{
		Name: "id",
		FromObject: func(a NodeAddress) index.KeySet {
			return index.NewKeySet(index.NetIPAddr(a.Addr))
		},
		FromKey: func(addr netip.Addr) []byte {
			return index.NetIPAddr(addr)
		},
		Unique: true,
	}

	// NodeAddressTestTableCell provides the node address Table and RWTable
	// for use in tests of modules that depend on node addresses.
	NodeAddressTestTableCell = statedb.NewTableCell[NodeAddress](
		NodeAddressTableName,
		NodeAddressIndex,
	)

	NodeAddressCell = cell.Module(
		"node-address",
		"Table of node addresses derived from system network devices",

		statedb.NewPrivateRWTableCell[NodeAddress](NodeAddressTableName, NodeAddressIndex),
		cell.Provide(
			newAddressScopeMax,
			newNodeAddressTable,
		),
		cell.Config(NodeAddressConfig{}),
	)
)

type AddressScopeMax uint8

func newAddressScopeMax(cfg NodeAddressConfig) (AddressScopeMax, error) {
	scope, err := ip.ParseScope(cfg.AddressScopeMax)
	if err != nil {
		return 0, fmt.Errorf("Cannot parse scope integer from --%s option", addressScopeMaxFlag)
	}
	return AddressScopeMax(scope), nil
}

type NodeAddressConfig struct {
	NodeAddresses []*cidr.CIDR `mapstructure:"node-addresses"`

	// AddressScopeMax controls the maximum address scope for addresses to be
	// considered local ones. Affects which addresses are used for NodePort
	// and which have HOST_ID in the ipcache.
	AddressScopeMax string `mapstructure:"local-max-addr-scope"`
}

const (
	addressScopeMaxFlag = "local-max-addr-scope"
)

func (cfg NodeAddressConfig) getNets() []*net.IPNet {
	nets := make([]*net.IPNet, len(cfg.NodeAddresses))
	for i, cidr := range cfg.NodeAddresses {
		nets[i] = cidr.IPNet
	}
	return nets
}

func (NodeAddressConfig) Flags(flags *pflag.FlagSet) {
	flags.StringSlice(
		"node-addresses",
		nil,
		"A whitelist of CIDRs to limit which IPs are considered node addresses. If not set, primary IP address of each native device is used.")

	flags.String(addressScopeMaxFlag, fmt.Sprintf("%d", defaults.AddressScopeMax), "Maximum local address scope for ipcache to consider host addresses")
	flags.MarkHidden(addressScopeMaxFlag)
}

type nodeAddressSource struct {
	cell.In

	HealthScope     cell.Scope
	Log             logrus.FieldLogger
	Config          NodeAddressConfig
	Lifecycle       hive.Lifecycle
	Jobs            job.Registry
	DB              *statedb.DB
	Devices         statedb.Table[*Device]
	NodeAddresses   statedb.RWTable[NodeAddress]
	AddressScopeMax AddressScopeMax
}

func newNodeAddressTable(n nodeAddressSource) (tbl statedb.Table[NodeAddress], err error) {
	n.register()
	return n.NodeAddresses, nil
}

func (n *nodeAddressSource) register() {
	g := n.Jobs.NewGroup(n.HealthScope)
	g.Add(job.OneShot("node-address-update", n.run))

	n.Lifecycle.Append(
		hive.Hook{
			OnStart: func(ctx hive.HookContext) error {
				// Do an immediate update to populate the table
				// before it is read from.
				n.update(ctx)

				// Start the background refresh.
				return g.Start(ctx)
			},
			OnStop: g.Stop,
		})

}

func (n *nodeAddressSource) run(ctx context.Context, reporter cell.HealthReporter) error {
	limiter := rate.NewLimiter(100*time.Millisecond, 1)
	for {
		watch, addrs := n.update(ctx)
		reporter.OK(addrs)
		select {
		case <-ctx.Done():
			return nil
		case <-watch:
		}
		if err := limiter.Wait(ctx); err != nil {
			return err
		}
	}
}

func (n *nodeAddressSource) update(ctx context.Context) (<-chan struct{}, string) {
	txn := n.DB.WriteTxn(n.NodeAddresses)
	defer txn.Abort()

	devs, watch := SelectedDevices(n.Devices, txn)

	old := n.getCurrentAddresses(txn)
	new := n.getAddressesFromDevices(devs)
	updated := false

	// Insert new addresses that did not exist.
	for addr := range new.Difference(old) {
		updated = true
		n.NodeAddresses.Insert(txn, addr)
	}

	// Remove addresses that were not part of the new set.
	for addr := range old.Difference(new) {
		updated = true
		n.NodeAddresses.Delete(txn, addr)
	}

	addrs := showAddresses(new)
	if updated {
		n.Log.WithField("node-addresses", addrs).Info("Node addresses updated")
		txn.Commit()
	}
	return watch, addrs
}

func (n *nodeAddressSource) getCurrentAddresses(txn statedb.ReadTxn) sets.Set[NodeAddress] {
	addrs := sets.New[NodeAddress]()
	iter, _ := n.NodeAddresses.All(txn)
	for addr, _, ok := iter.Next(); ok; addr, _, ok = iter.Next() {
		addrs.Insert(addr)
	}
	return addrs
}

func (n *nodeAddressSource) getAddressesFromDevices(devs []*Device) (addrs sets.Set[NodeAddress]) {
	addrs = sets.New[NodeAddress]()
	for _, dev := range devs {
		for _, addr := range dev.Addrs {
			// Keep the scope-based address filtering as was introduced
			// in 080857bdedca67d58ec39f8f96c5f38b22f6dc0b.
			if addr.Scope > uint8(n.AddressScopeMax) {
				fmt.Printf("skipped %s: %d > %d\n", addr.Addr, addr.Scope, n.AddressScopeMax)
				continue
			}
			if addr.Addr.IsLoopback() {
				continue
			}

			if len(n.Config.NodeAddresses) == 0 {
				addrs.Insert(NodeAddress{Addr: addr.Addr, DeviceName: dev.Name})

				// The default behavior when --nodeport-addresses is not set is
				// to only use the primary IP of each device, so stop here.
				break
			} else if ip.NetsContainsAny(n.Config.getNets(), []*net.IPNet{ip.IPToPrefix(addr.AsIP())}) {
				addrs.Insert(NodeAddress{Addr: addr.Addr, DeviceName: dev.Name})
			}
		}
	}
	return
}

// showAddresses formats a Set[NodeAddress] as "1.2.3.4 (eth0), fe80::1 (eth1)"
func showAddresses(addrs sets.Set[NodeAddress]) string {
	ss := make([]string, 0, len(addrs))
	for addr := range addrs {
		ss = append(ss, addr.String())
	}
	return strings.Join(ss, ", ")
}
