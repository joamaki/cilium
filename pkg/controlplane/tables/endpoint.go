package tables

import (
	"net"

	"github.com/hashicorp/go-memdb"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"

	"github.com/cilium/cilium/pkg/endpoint"
	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/statedb"
)

// Endpoint is a Cilium endpoint that can be either local or remote.
type Endpoint struct {
	Namespace, Name string

	Labels labels.Labels

	// NodeIP is the IP of the Cilium node hosting the endpoint
	NodeIP net.IP

	// Identity associated with the endpoint.
	Identity identity.NumericIdentity

	// Addresses are the list of addresses associated with the endpoint.
	Addresses []net.IP

	// Local if not nil points to the local endpoint entity
	// that manages the BPF state related to it.
	Local *endpoint.Endpoint
}

func (e *Endpoint) DeepCopy() *Endpoint {
	e2 := *e
	e2.Labels = maps.Clone(e2.Labels)
	e2.Addresses = slices.Clone(e2.Addresses)
	return &e2
}

var endpointTableSchema = &memdb.TableSchema{
	Name: "endpoints",
	Indexes: map[string]*memdb.IndexSchema{
		// Primary index for endpoints is the namespace and name pair.
		"id": {
			Name:         "id",
			AllowMissing: false,
			Unique:       true,
			Indexer: &memdb.CompoundIndex{
				Indexes: []memdb.Indexer{
					&memdb.StringFieldIndex{Field: "Namespace"},
					&memdb.StringFieldIndex{Field: "Name"},
				},
			},
		},
		"namespace": {
			Name:         "namespace",
			AllowMissing: false,
			Unique:       false,
			Indexer:      &memdb.StringFieldIndex{Field: "Namespace"},
		},

		"local": {
			Name: "local",
			Indexer: &memdb.ConditionalIndex{
				Conditional: func(obj any) (bool, error) {
					return obj.(*Endpoint).Local != nil, nil
				},
			},
		},
	},
}

func LocalEndpoints() statedb.Query {
	return statedb.Query{Index: "local", Args: []any{true}}
}

func EndpointByName(namespace, name string) statedb.Query {
	return statedb.Query{Index: "id", Args: []any{namespace, name}}

}
