package state

import (
	. "github.com/cilium/cilium/memdb/state/structs"
	"github.com/cilium/cilium/pkg/hive/cell"
	memdb "github.com/hashicorp/go-memdb"
)

var Cell = cell.Module(
	"state",
	"Das State",

	cell.Provide(
		New,
		func() tablesOut { return tables },
	),
)

type tablesOut struct {
	cell.Out

	Nodes              Table[*Node]
	ExtNetworkPolicies Table[*ExtNetworkPolicy]
	ExtPolicyRules     Table[*ExtPolicyRule]
	Endpoints          Table[*Endpoint]
	SelectorPolicies   Table[*SelectorPolicy]
}

var tables = tablesOut{
	Nodes:              &table[*Node]{nodeTable},
	ExtNetworkPolicies: &table[*ExtNetworkPolicy]{extNetworkPolicyTable},
	Endpoints:          &table[*Endpoint]{endpointTable},
	ExtPolicyRules:     &table[*ExtPolicyRule]{extPolicyRuleTable},
	SelectorPolicies:   &table[*SelectorPolicy]{selectorPolicyTable},
}

type tableSchemasOut struct {
	cell.Out

	Schemas []*memdb.TableSchema `group:"state-table-schemas,flatten"`
}

func TableSchemas(schemas ...*memdb.TableSchema) func() tableSchemasOut {
	return func() tableSchemasOut {
		return tableSchemasOut{Schemas: schemas}
	}
}
