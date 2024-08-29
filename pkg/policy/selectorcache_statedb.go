package policy

import (
	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/policy/api"
	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
)

type Selector struct {
	EndpointSelector api.EndpointSelector
	Selections       identity.NumericIdentitySlice
}

var (
	SelectorLabelIndex = statedb.Index[*Selector, *api.EndpointSelector]{
		Name: "key",
		FromObject: func(obj *Selector) index.KeySet {
			return index.NewKeySet(index.String(obj.EndpointSelector.CachedString()))
		},
		FromKey: func(es *api.EndpointSelector) index.Key {
			return index.String(es.CachedString())
		},
		Unique: true,
	}

	SelectorByLabels = SelectorLabelIndex.Query

	// keys are:
	// /<id1>-<labels1>
	// /<id1>-<labels2>
	// /<id2>-<labels1>
	// /<id2>-<labels3>
	// ...

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
		Unique: false,
	}

	SelectorsByIdentity = SelectorIdentityIndex.Query
)

func NewSelectorTable(db *statedb.DB) (statedb.RWTable[*Selector], error) {
	tbl, err := statedb.NewTable(
		"selectors",
		SelectorLabelIndex,
		SelectorIdentityIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}
