package policy

import (
	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/policy/api"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
)

type Selector struct {
	EndpointSelector api.EndpointSelector
	Labels           labels.LabelArray
	Selections       identity.NumericIdentitySlice
}

var (
	SelectorIdentityIndex = statedb.Index[*Selector, identity.NumericIdentity]{
		Name: "identity",
		FromObject: func(obj *Selector) index.KeySet {
			ks := make([]index.Key, len(obj.Selections))
			for i := range obj.Selections {
				ks[i] = index.Uint32(uint32(obj.Selections[i]))
			}
			return index.NewKeySet(ks...)
		},
		FromKey: func(key identity.NumericIdentity) index.Key {
			return index.Uint32(uint32(key))
		},
		Unique: true,
	}

	SelectorByIdentity = SelectorIdentityIndex.Query

	SelectorESIndex = statedb.Index[*Selector, *api.EndpointSelector]{
		Name: "key",
		FromObject: func(obj *Selector) index.KeySet {
			return index.NewKeySet(index.String(obj.EndpointSelector.CachedString()))
		},
		FromKey: func(es *api.EndpointSelector) index.Key { return index.String(es.CachedString()) },
		Unique:  true,
	}

	SelectorByES = SelectorESIndex.Query
)

func NewSelectorTable(db *statedb.DB) (statedb.RWTable[*Selector], error) {
	tbl, err := statedb.NewTable(
		"selectors",
		SelectorESIndex,
		SelectorIdentityIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}

type DBCachedSelector struct {
	es    api.EndpointSelector
	s     *Selector
	tbl   statedb.Table[*Selector]
	watch <-chan struct{}
}

func (ds *DBCachedSelector) Get(txn statedb.ReadTxn) *Selector {
	select {
	case <-ds.watch:
		// Watch channel closed, requery for the latest one. This gets
		// the version in the snapshot ([txn]), which may not be latest
		// if the snapshot is old. A top-level controller's job would be
		// to recompute with a fresh snapshot when there's changes in the table.
		ds.s, _, ds.watch, _ = ds.tbl.GetWatch(txn, SelectorByES(&ds.es))
	default:
		// Up to date.
	}
	return ds.s
}

func GetDBCachedSelector(txn statedb.ReadTxn, tbl statedb.Table[*Selector], es *api.EndpointSelector) *DBCachedSelector {
	var ds DBCachedSelector
	ds.es = *es
	ds.tbl = tbl
	ds.s, _, ds.watch, _ = tbl.GetWatch(txn, SelectorByES(es))
	if ds.s != nil {
		return &ds
	}
	return nil
}
