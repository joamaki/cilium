package tables

import (
	"github.com/hashicorp/go-memdb"

	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/ipcache"
	"github.com/cilium/cilium/pkg/source"
	"github.com/cilium/cilium/pkg/statedb"
)

type Identity struct {
	ipcache.Identity

	IP string

	K8sMeta *ipcache.K8sMetadata
}

func (i *Identity) DeepCopy() *Identity {
	return &Identity{
		Identity: ipcache.Identity{
			ID:     i.ID,
			Source: i.Source,
		},
		IP: i.IP,
	}
}

var identityTableSchema = &memdb.TableSchema{
	Name: "identities",
	Indexes: map[string]*memdb.IndexSchema{
		// The primary key for identities is the IP. Multiple IPs can
		// point to the same identity.
		"id": {
			Name:         "id",
			AllowMissing: false,
			Unique:       true,
			Indexer:      &memdb.StringFieldIndex{Field: "IP"},
		},
		"identity": {
			Name:         "identity",
			AllowMissing: false,
			Unique:       false,
			Indexer:      &memdb.UintFieldIndex{Field: "ID"},
		},
		"source": {
			Name:         "source",
			AllowMissing: false,
			Unique:       false,
			Indexer:      &memdb.StringFieldIndex{Field: "Source"},
		},
	},
}

func IdentitiesByIP(ip string) statedb.Query {
	return statedb.Query{Index: "id", Args: []any{ip}}
}

func IdentitiesByID(id identity.NumericIdentity) statedb.Query {
	return statedb.Query{Index: "identity", Args: []any{id}}
}

func IdentitiesBySource(src source.Source) statedb.Query {
	return statedb.Query{Index: "source", Args: []any{src}}
}
