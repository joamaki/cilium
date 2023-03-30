package linux

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"

	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/ip"
	"github.com/cilium/cilium/pkg/statedb"
)

var DevicesControllerCell = cell.Module(
	"datapath-devices-controller",
	"Populates the devices table from netlink",

	cell.Invoke(registerDevicesController),
)

// addrUpdateBatchingDuration is the amount of time to wait for more
// netlink.AddrUpdate messages before processing the batch.
var addrUpdateBatchingDuration = 100 * time.Millisecond

type devicesControllerParams struct {
	cell.In

	Log          logrus.FieldLogger
	DB           statedb.DB
	DevicesTable statedb.Table[*tables.Device]
}

func registerDevicesController(lc hive.Lifecycle, p devicesControllerParams) {
	dc := &devicesController{devicesControllerParams: p}
	dc.ctx, dc.cancel = context.WithCancel(context.Background())
	lc.Append(dc)
}

type devicesController struct {
	devicesControllerParams
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (dc *devicesController) Start(hive.HookContext) error {
	updates := make(chan netlink.AddrUpdate, 16)
	opts := netlink.AddrSubscribeOptions{
		ListExisting: true,

		// Set a timeout as otherwise we would be blocking on RecvFrom. Closing
		// the netlink socket is not enough.
		// Fixed by https://github.com/vishvananda/netlink/pull/793, which
		// isn't yet merged.
		ReceiveTimeout: &unix.Timeval{Sec: 1},
	}
	if err := netlink.AddrSubscribeWithOptions(updates, dc.ctx.Done(), opts); err != nil {
		return err
	}
	dc.wg.Add(1)
	go dc.loop(updates)
	return nil
}

func (dc *devicesController) Stop(hive.HookContext) error {
	dc.cancel()
	dc.wg.Wait()
	return nil
}

func (dc *devicesController) loop(updates chan netlink.AddrUpdate) {
	defer dc.wg.Done()
	timer := time.NewTimer(addrUpdateBatchingDuration)
	if !timer.Stop() {
		<-timer.C
	}
	timerStopped := true

	for {
		buf := map[int][]netlink.AddrUpdate{}
		for {
			select {
			case u, ok := <-updates:
				if !ok {
					return
				}

				buf[u.LinkIndex] = append(buf[u.LinkIndex], u)
				if timerStopped {
					timer.Reset(addrUpdateBatchingDuration)
					timerStopped = false
				}
			case <-timer.C:
				timerStopped = true

				txn := dc.DB.WriteTxn()
				w := dc.DevicesTable.Writer(txn)

				var err error
				for index, updates := range buf {
					d, _ := w.First(tables.ByIndex(index))
					if d == nil {
						d = &tables.Device{
							Index: index,
						}
					} else {
						d = d.DeepCopy()
					}
					for _, u := range updates {
						if u.NewAddr {
							d.IPs = append(d.IPs, ip.MustAddrFromIP(u.LinkAddress.IP))
						} else {
							i := slices.Index(d.IPs, ip.MustAddrFromIP(u.LinkAddress.IP))
							if i >= 0 {
								d.IPs = slices.Delete(d.IPs, i, i+1)
							}
						}
					}
					err = w.Insert(d)
					if err != nil {
						log.WithError(err).Error("Insert into devices table failed")
						break
					}
				}
				if err != nil {
					txn.Abort()
				} else if err = txn.Commit(); err != nil {
					log.WithError(err).Error("Committing to devices table failed")
				} else {
					buf = map[int][]netlink.AddrUpdate{}
				}
			}
		}

	}
}
