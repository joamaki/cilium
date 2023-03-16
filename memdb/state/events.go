package state

// TODO do we want to expose the table name constants?

type Event struct {
	table string // The table that changed
}

func (e Event) ForEndpointTable() bool {
	return e.table == endpointTable
}

func (e Event) ForExtPolicyRules() bool {
	return e.table == extPolicyRuleTable
}

func (e Event) ForSelectorPolicies() bool {
	return e.table == selectorPolicyTable
}

func (e Event) ForTable(table string) bool {
	return e.table == table
}
