package controllers

import (
	"fmt"
	"net"

	cmtypes "github.com/cilium/cilium/pkg/clustermesh/types"
	"github.com/cilium/cilium/pkg/controlplane/tables"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/ipcache"
	"github.com/cilium/cilium/pkg/statedb"
)

var identitiesControllerCell = cell.Invoke(registerIPCacheToIdentities)

type ipcacheListener struct {
	db    statedb.DB
	table statedb.Table[*tables.Identity]
}

// OnIPIdentityCacheChange implements ipcache.IPIdentityMappingListener
func (l *ipcacheListener) OnIPIdentityCacheChange(
	modType ipcache.CacheModification, cidrCluster cmtypes.PrefixCluster,
	oldHostIP net.IP, newHostIP net.IP,
	oldID *ipcache.Identity, newID ipcache.Identity,
	encryptKey uint8, nodeID uint16, k8sMeta *ipcache.K8sMetadata) {

	txn := l.db.WriteTxn()
	w := l.table.Writer(txn)
	if modType == ipcache.Upsert {
		fmt.Printf(">>> Upserted identity %d => %s\n", newID.ID, newHostIP)
		err := w.Insert(&tables.Identity{
			Identity: newID,
			IP:       newHostIP.String(),
			K8sMeta:  k8sMeta,
		})
		if err != nil {
			panic(err)
		}
	} else {
		fmt.Printf(">>> Deleted identity %d => %s\n", oldID.ID, oldHostIP)
		err := w.Delete(&tables.Identity{IP: oldHostIP.String()})
		if err != nil {
			panic(err)
		}
	}
	txn.Commit()
}

// OnIPIdentityCacheGC implements ipcache.IPIdentityMappingListener
func (*ipcacheListener) OnIPIdentityCacheGC() {
}

var _ ipcache.IPIdentityMappingListener = &ipcacheListener{}

func registerIPCacheToIdentities(ipc *ipcache.IPCache, db statedb.DB, identitiesTable statedb.Table[*tables.Identity]) {
	ipc.AddListener(&ipcacheListener{db, identitiesTable})
}
