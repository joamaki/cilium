// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

// Package quota contains generic quota state shared by Cilium components.
package quota

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cilium/cilium/pkg/source"

	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
)

// ResourceName identifies a quota within a namespace.
// For quotas sourced from Kubernetes ResourceQuotas this is the key
// of the quota, including the "cilium.io/" prefix.
type ResourceName string

// ResourceQuota contains the effective limit and current usage for a
// namespaced resource.
type ResourceQuota struct {
	// Namespace the quota applies to.
	Namespace string

	// Name of the quota resource.
	Name ResourceName

	// Hard is the effective limit derived from Inputs.
	Hard uint64

	// Used is the current usage.
	Used uint64

	// Inputs are the limits contributed by quota sources. They are sorted by
	// source priority, with the winning input at index zero.
	Inputs []ResourceQuotaInput
}

// ResourceQuotaInput is a limit contributed by a source object.
type ResourceQuotaInput struct {
	Source source.Source
	Origin string
	Hard   uint64
}

// Unlimited is the effective limit when no inputs are configured.
const Unlimited = ^uint64(0)

var sourcePriorities = func() map[source.Source]int {
	sources := source.NewSources()
	priorities := make(map[source.Source]int, len(sources))
	for priority, source := range sources {
		priorities[source] = priority
	}
	return priorities
}()

// AddInput adds or replaces an input from the same source and origin, then
// recomputes the effective hard limit.
func (q *ResourceQuota) AddInput(input ResourceQuotaInput) {
	inputs := slices.Clone(q.Inputs)
	for i := range inputs {
		if inputs[i].Source == input.Source && inputs[i].Origin == input.Origin {
			inputs[i] = input
			q.Inputs = sortQuotaInputs(inputs)
			q.updateHard()
			return
		}
	}
	q.Inputs = sortQuotaInputs(append(inputs, input))
	q.updateHard()
}

// RemoveInput removes the input from the specified source and origin, then
// recomputes the effective hard limit. It reports whether an input was found.
func (q *ResourceQuota) RemoveInput(source source.Source, origin string) bool {
	inputs := make([]ResourceQuotaInput, 0, len(q.Inputs))
	found := false
	for _, input := range q.Inputs {
		if input.Source == source && input.Origin == origin {
			found = true
			continue
		}
		inputs = append(inputs, input)
	}
	if !found {
		return false
	}
	q.Inputs = sortQuotaInputs(inputs)
	q.updateHard()
	return true
}

// Origin returns the source and origin of the winning input. It returns empty
// values when no inputs are configured.
func (q ResourceQuota) Origin() (source.Source, string) {
	if len(q.Inputs) == 0 {
		return "", ""
	}
	return q.Inputs[0].Source, q.Inputs[0].Origin
}

func sortQuotaInputs(inputs []ResourceQuotaInput) []ResourceQuotaInput {
	slices.SortFunc(inputs, func(a, b ResourceQuotaInput) int {
		priorityA, foundA := sourcePriorities[a.Source]
		if !foundA {
			priorityA = len(sourcePriorities)
		}
		priorityB, foundB := sourcePriorities[b.Source]
		if !foundB {
			priorityB = len(sourcePriorities)
		}
		if priorityA != priorityB {
			return priorityA - priorityB
		}
		if a.Source != b.Source {
			return strings.Compare(string(a.Source), string(b.Source))
		}
		if a.Hard != b.Hard {
			if a.Hard < b.Hard {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Origin, b.Origin)
	})
	return inputs
}

func (q *ResourceQuota) updateHard() {
	if len(q.Inputs) == 0 {
		q.Hard = Unlimited
		return
	}
	q.Hard = q.Inputs[0].Hard
}

// TableHeader implements statedb.TableWritable.
func (q ResourceQuota) TableHeader() []string {
	return []string{
		"Namespace",
		"Name",
		"Source",
		"Origin",
		"Hard",
		"Used",
		"Inputs",
	}
}

// TableRow implements statedb.TableWritable.
func (q ResourceQuota) TableRow() []string {
	source, origin := q.Origin()
	inputs := make([]string, 0, len(q.Inputs))
	for _, input := range q.Inputs {
		inputs = append(inputs, fmt.Sprintf("%s:%s=%d", input.Source, input.Origin, input.Hard))
	}
	return []string{
		q.Namespace,
		string(q.Name),
		string(source),
		origin,
		strconv.FormatUint(q.Hard, 10),
		strconv.FormatUint(q.Used, 10),
		strings.Join(inputs, ","),
	}
}

var (
	resourceQuotaNameIndex = statedb.Index[ResourceQuota, string]{
		Name: "name",
		FromObject: func(quota ResourceQuota) index.KeySet {
			return index.NewKeySet(index.String(quota.Namespace + "/" + string(quota.Name)))
		},
		FromKey:    index.String,
		FromString: index.FromString,
		Unique:     true,
	}
)

// ResourceQuotaByName queries a quota by namespace and quota name.
func ResourceQuotaByName(namespace string, quota ResourceName) statedb.Query[ResourceQuota] {
	return resourceQuotaNameIndex.Query(namespace + "/" + string(quota))
}

// TableName is the StateDB table name for ResourceQuota objects.
const TableName = "resource-quotas"

// NewTable creates the ResourceQuota StateDB table.
func NewTable(db *statedb.DB) (statedb.RWTable[ResourceQuota], error) {
	return statedb.NewTable(db, TableName, resourceQuotaNameIndex)
}
