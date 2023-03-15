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

type TableSchemaOut struct {
	cell.Out

	Schema *memdb.TableSchema `group:"state-table-schemas"`
}
