package state

import (
	"fmt"
	"net/netip"

	memdb "github.com/hashicorp/go-memdb"

	"github.com/cilium/cilium/pkg/labels"
)

type Index string

// TODO export the table names and use a newtype so that we can use that in the
// Event.
const (
	// "state" updated from external data sources. these
	// are lightweight versions of upstream objects such as the
	// Kubernetes Service and only contain the relevant information.
	// Most but not all of these tables should be only updated by
	// the data source, but some can be modified to reflect back changes
	// to the data source.
	//
	// control-plane will watch these and update its internal
	// state which then is fed to datapath.
	// (TODO better prefix)
	extNetworkPolicyTable  = "ext-network-policies"
	extPolicyRuleTable     = "ext-policy-rules"
	extPodTable            = "ext-pods"
	extServiceTable        = "ext-services"
	extEndpointTable       = "ext-endpoints"
	extCiliumEndpointTable = "ext-cilium-endpoints"
	extCiliumNodeTable     = "ext-cilium-nodes"
	extCiliumIdentityTable = "ext-cilium-identities"

	// control-plane internal state:
	nodeTable           = "int-nodes"
	ipcacheTable        = "int-ipcache"
	endpointTable       = "int-endpoints"
	selectorPolicyTable = "int-selector-policies"

	// datapath state:
	datapathEndpointTable = "dp-endpoints"
)

const (
	NameIndex      = Index("name")
	NamespaceIndex = Index("namespace")
	IDIndex        = Index("id")
	IPIndex        = Index("ip")
	IdentityIndex  = Index("identity")
	RuleIndex      = Index("rules")
	SelectorIndex  = Index("selector")
	RevisionIndex  = Index("revision")
	LabelKeyIndex  = Index("label-key")
)

var allTables = []func() *memdb.TableSchema{
	// external tables
	extNetworkPolicyTableSchema,
	extPolicyRuleTableSchema,

	// internal tables
	nodeTableSchema,
	ipcacheTableSchema,
	selectorPolicyTableSchema,
	endpointTableSchema,

	// datapath tables. should these be a different DB? e.g. control-plane
	// would distill state changes into a "datapath action event stream",
	// and datapath would internally update its own state based on that?
	// Feels important to keep the two layers separate for reuse.
	datapathEndpointTableSchema,
}

func schema() *memdb.DBSchema {
	dbSchema := &memdb.DBSchema{
		Tables: make(map[string]*memdb.TableSchema),
	}
	for _, sfn := range allTables {
		s := sfn()
		dbSchema.Tables[s.Name] = s
	}
	return dbSchema
}

func extNetworkPolicyTableSchema() *memdb.TableSchema {
	return &memdb.TableSchema{
		Name: extNetworkPolicyTable,
		Indexes: map[string]*memdb.IndexSchema{
			"id":                   idIndexSchema,
			string(NameIndex):      nameIndexSchema,
			string(NamespaceIndex): namespaceIndexSchema,
			string(RevisionIndex):  revisionIndexSchema,
		},
	}
}

func extPolicyRuleTableSchema() *memdb.TableSchema {
	return &memdb.TableSchema{
		Name: extPolicyRuleTable,
		Indexes: map[string]*memdb.IndexSchema{
			"id":                  idIndexSchema,
			string(NameIndex):     nameIndexSchema,
			string(RevisionIndex): revisionIndexSchema,
		},
	}
}

func ipcacheTableSchema() *memdb.TableSchema {
	return &memdb.TableSchema{
		Name: ipcacheTable,
		Indexes: map[string]*memdb.IndexSchema{
			"id": {
				Name:         "id",
				AllowMissing: false,
				Unique:       true,
				Indexer:      &memdb.UintFieldIndex{Field: "ID"},
			},
			string(IPIndex): {
				Name:         string(IPIndex),
				AllowMissing: false,
				Unique:       true,
				Indexer:      ipIndexer{},
			},
		},
	}
}

/*
func l4PolicyTableSchema() *memdb.TableSchema {
	return &memdb.TableSchema{
		Name: l4PolicyTable,
		Indexes: map[string]*memdb.IndexSchema{
			"id": idIndexSchema,
			string(RuleIndex): &memdb.IndexSchema{
				Name:         string(RuleIndex),
				AllowMissing: true,
				Unique:       false,
				Indexer:      &memdb.StringSliceFieldIndex{Field: "SourceRules"},
			},
		},
	}
}*/

func nodeTableSchema() *memdb.TableSchema {
	return &memdb.TableSchema{
		Name: nodeTable,
		Indexes: map[string]*memdb.IndexSchema{
			string(IDIndex):        idIndexSchema,
			string(NamespaceIndex): namespaceIndexSchema,
			string(NameIndex):      nameIndexSchema,
		},
	}
}

func selectorPolicyTableSchema() *memdb.TableSchema {
	return &memdb.TableSchema{
		Name: selectorPolicyTable,
		Indexes: map[string]*memdb.IndexSchema{
			string(IDIndex):       idIndexSchema,
			string(RevisionIndex): revisionIndexSchema,
			string(LabelKeyIndex): {
				Name:         string(LabelKeyIndex),
				AllowMissing: false,
				Unique:       true,
				Indexer:      &memdb.StringFieldIndex{Field: "LabelKey"},
			},
			// TODO NumericIdentity index
		},
	}
}

func endpointTableSchema() *memdb.TableSchema {
	return &memdb.TableSchema{
		Name: endpointTable,
		Indexes: map[string]*memdb.IndexSchema{
			string(IDIndex): idIndexSchema,
			string(LabelKeyIndex): {
				Name:         string(LabelKeyIndex),
				AllowMissing: false,
				Unique:       true,
				Indexer:      &memdb.StringFieldIndex{Field: "LabelKey"},
			},
			string(RevisionIndex): revisionIndexSchema,
			// TODO 'State' index?
		},
	}
}

func datapathEndpointTableSchema() *memdb.TableSchema {
	return &memdb.TableSchema{
		Name: datapathEndpointTable,
		Indexes: map[string]*memdb.IndexSchema{
			string(IDIndex): idIndexSchema,
		},
	}
}

var idIndexSchema = &memdb.IndexSchema{
	Name:         "id",
	AllowMissing: false,
	Unique:       true,
	Indexer:      &memdb.UUIDFieldIndex{Field: "ID"},
}

var namespaceIndexSchema = &memdb.IndexSchema{
	Name:         string(NamespaceIndex),
	AllowMissing: true,
	Unique:       false,
	Indexer:      &memdb.StringFieldIndex{Field: "Namespace"},
}

var nameIndexSchema = &memdb.IndexSchema{
	Name:         string(NameIndex),
	AllowMissing: false,
	Unique:       true,
	Indexer:      nameIndexer{},
}

var revisionIndexSchema = &memdb.IndexSchema{
	Name:         string(RevisionIndex),
	AllowMissing: false,
	Unique:       false,
	Indexer:      &memdb.UintFieldIndex{Field: "Revision"},
}

// nameIndexer implements <namespace>/<name> indexing for all objects
// that implement MetaGetter or embed Meta.
type nameIndexer struct{}

func (nameIndexer) FromObject(obj interface{}) (bool, []byte, error) {
	meta, ok := obj.(ExtMetaGetter)
	if !ok {
		return false, nil,
			fmt.Errorf("object %T does not implement MetaGetter", obj)
	}

	idx := meta.GetNamespace() + "/" + meta.GetName() + "\x00"
	return true, []byte(idx), nil
}

func (nameIndexer) FromArgs(args ...interface{}) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("must provide only a single argument")
	}
	arg, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("argument must be a string: %#v", args[0])
	}
	arg += "\x00"
	return []byte(arg), nil
}

func (m nameIndexer) PrefixFromArgs(args ...interface{}) ([]byte, error) {
	val, err := m.FromArgs(args...)
	if err != nil {
		return nil, err
	}

	// Strip the null terminator, the rest is a prefix
	n := len(val)
	if n > 0 {
		return val[:n-1], nil
	}
	return val, nil
}

type ipIndexer struct{}

func (ipIndexer) FromObject(obj interface{}) (bool, []byte, error) {
	ipg, ok := obj.(IPGetter)
	if !ok {
		return false, nil,
			fmt.Errorf("object %T does not implement IPGetter", obj)
	}
	idx := ipg.GetIP().AsSlice()
	return true, idx, nil
}

func (ipIndexer) FromArgs(args ...interface{}) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("must provide only a single argument")
	}
	arg, ok := args[0].(netip.Addr)
	if !ok {
		return nil, fmt.Errorf("argument must be a netip.Addr: %#v", args[0])
	}
	return arg.AsSlice(), nil
}

func (m ipIndexer) PrefixFromArgs(args ...interface{}) ([]byte, error) {
	val, err := m.FromArgs(args...)
	if err != nil {
		return nil, err
	}
	return val, nil
}

type Query struct {
	Index Index
	Args  []any
}

func ByName(namespace string, name string) Query {
	return Query{NameIndex, []any{namespace + "/" + name}}
}

func ByNamespace(namespace string) Query {
	return Query{NamespaceIndex, []any{namespace}}
}

// ByID queries by the "ID" field. The type of "ID" is table specific, thus
// this function takes an 'any'.
func ByID(id any) Query {
	return Query{IDIndex, []any{id}}
}

func ByIdentity(id uint64) Query {
	return Query{IdentityIndex, []any{id}}
}

func ByRevision(rev uint64) Query {
	return Query{RevisionIndex, []any{rev}}
}

func BySelector(sel string) Query {
	return Query{SelectorIndex, []any{sel}}
}

func ByLabels(labels labels.Labels) Query {
	return Query{LabelKeyIndex, []any{LabelKey(labels.SortedList())}}
}

func ByLabelKey(key LabelKey) Query {
	return Query{LabelKeyIndex, []any{key}}
}

var All = Query{"id", []any{}}
