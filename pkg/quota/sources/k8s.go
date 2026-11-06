// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quotasources

import (
	"iter"
	"slices"
	"strings"

	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"
	corev1 "k8s.io/api/core/v1"

	"github.com/cilium/cilium/pkg/k8s"
	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/utils"
	"github.com/cilium/cilium/pkg/quota"
	"github.com/cilium/cilium/pkg/source"
)

const resourceNamePrefix = "cilium.io/"

// NewTableAndReflector creates the ResourceQuota table and registers the
// Kubernetes ResourceQuota reflector.
func NewTableAndReflector(jg job.Group, db *statedb.DB, cs client.Clientset) (statedb.RWTable[quota.ResourceQuota], error) {
	table, err := quota.NewTable(db)
	if err != nil {
		return nil, err
	}

	if !cs.IsEnabled() {
		return table, nil
	}

	config := k8s.ReflectorConfig[quota.ResourceQuota]{
		Name:          "resourcequotas",
		Table:         table,
		ListerWatcher: utils.ListerWatcherFromTyped(cs.CoreV1().ResourceQuotas("")),
		MetricScope:   "ResourceQuota",
		OnReplace: func(txn statedb.WriteTxn) {
			// The replacement list contains only currently existing Kubernetes
			// objects. Remove the old Kubernetes inputs first while preserving
			// usage and inputs owned by other sources.
			rows := make([]quota.ResourceQuota, 0)
			for resourceQuota := range table.All(txn) {
				rows = append(rows, resourceQuota)
			}
			for _, resourceQuota := range rows {
				origins := make(map[string]struct{})
				for _, input := range resourceQuota.Inputs {
					if input.Source == source.Kubernetes {
						origins[input.Origin] = struct{}{}
					}
				}
				for origin := range origins {
					resourceQuota.RemoveInput(source.Kubernetes, origin)
				}

				if len(origins) == 0 {
					continue
				}
				if len(resourceQuota.Inputs) == 0 && resourceQuota.Used == 0 {
					_, _, _ = table.Delete(txn, resourceQuota)
				} else {
					_, _, _ = table.Insert(txn, resourceQuota)
				}
			}
		},
		// OnReplace removes stale Kubernetes inputs first. The default
		// replacement cleanup must not consider rows owned by other sources or
		// usage-only rows for deletion.
		QueryAll: func(statedb.ReadTxn, statedb.Table[quota.ResourceQuota]) iter.Seq2[quota.ResourceQuota, statedb.Revision] {
			return func(func(quota.ResourceQuota, statedb.Revision) bool) {}
		},
		TransformMany: func(txn statedb.ReadTxn, deleted bool, obj any) (iter.Seq[quota.ResourceQuota], iter.Seq[quota.ResourceQuota]) {
			rq, ok := obj.(*corev1.ResourceQuota)
			if !ok {
				return nil, nil
			}

			objectKey := rq.Namespace + "/" + rq.Name
			current := make(map[quota.ResourceName]uint64)
			if !deleted {
				for resourceName, quantity := range rq.Spec.Hard {
					if strings.HasPrefix(string(resourceName), resourceNamePrefix) {
						current[quota.ResourceName(resourceName)] = uint64(quantity.Value())
					}
				}
			}

			// Find the resources previously contributed by this object. The
			// inputs are stored in the table, so no parallel reflector state is
			// needed to process updates, deletes, or replacements.
			changedNames := make(map[quota.ResourceName]struct{}, len(current))
			for existing := range table.All(txn) {
				if existing.Namespace != rq.Namespace {
					continue
				}
				for _, input := range existing.Inputs {
					if input.Source == source.Kubernetes && input.Origin == objectKey {
						changedNames[existing.Name] = struct{}{}
						break
					}
				}
			}
			for name := range current {
				changedNames[name] = struct{}{}
			}

			inserts := make([]quota.ResourceQuota, 0, len(changedNames))
			deletes := make([]quota.ResourceQuota, 0, len(changedNames))
			for name := range changedNames {
				resourceQuota, _, found := table.Get(txn, quota.ResourceQuotaByName(rq.Namespace, name))
				if !found {
					resourceQuota = quota.ResourceQuota{
						Namespace: rq.Namespace,
						Name:      name,
						Hard:      quota.Unlimited,
					}
				}

				if hard, exists := current[name]; exists {
					resourceQuota.AddInput(quota.ResourceQuotaInput{
						Source: source.Kubernetes,
						Origin: objectKey,
						Hard:   hard,
					})
				} else {
					resourceQuota.RemoveInput(source.Kubernetes, objectKey)
				}

				if len(resourceQuota.Inputs) == 0 && resourceQuota.Used == 0 {
					if found {
						deletes = append(deletes, resourceQuota)
					}
					continue
				}
				inserts = append(inserts, resourceQuota)
			}

			return slices.Values(inserts), slices.Values(deletes)
		},
	}
	if err := k8s.RegisterReflector(jg, db, config); err != nil {
		return nil, err
	}
	return table, nil
}
