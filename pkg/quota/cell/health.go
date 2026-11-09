// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quotacell

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cilium/cilium/pkg/quota"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/statedb"
)

// observeHealth publishes health and excess metrics from committed changes.
// Aborted Accounting transactions cannot leak reporting changes. No errors are logged.
func observeHealth(jobs job.Group, db *statedb.DB, table statedb.Table[quota.ResourceQuota], metrics *quotaMetrics) {
	jobs.Add(job.OneShot("quota-health", func(ctx context.Context, health cell.Health) error {
		type key struct {
			namespace string
			resource  quota.ResourceName
		}

		txn := db.WriteTxn(table)
		changes, err := table.Changes(txn)
		if err != nil {
			txn.Abort()
			return err
		}
		txn.Commit()
		defer changes.Close()
		// Retain only exceeded quotas and process changed rows, avoiding a
		// full table scan on every reservation update.
		active := map[key]string{}
		defer func() {
			for key := range active {
				metrics.Excess.DeleteLabelValues(key.namespace, string(key.resource))
			}
		}()
		for {
			updates, watch := changes.Next(db.ReadTxn())
			for change := range updates {
				row := change.Object
				key := key{row.Namespace, row.Name}
				if change.Deleted || row.Used <= row.Hard {
					if _, exists := active[key]; exists {
						delete(active, key)
						metrics.Excess.DeleteLabelValues(key.namespace, string(key.resource))
					}
					continue
				}
				active[key] = fmt.Sprintf("namespace=%q resource=%q used=%d hard=%d", row.Namespace, row.Name, row.Used, row.Hard)
				metrics.Excess.WithLabelValues(row.Namespace, string(row.Name)).Set(float64(row.Used - row.Hard))
			}
			exceeded := make([]string, 0, len(active))
			for _, description := range active {
				exceeded = append(exceeded, description)
			}
			slices.Sort(exceeded)

			if len(exceeded) == 0 {
				health.OK("All resource quotas are within limits")
			} else {
				message := fmt.Sprintf("%d resource quotas exceeded", len(exceeded))
				if len(exceeded) == 1 {
					message = "1 resource quota exceeded"
				}
				health.Degraded(message, errors.New(strings.Join(exceeded, "; ")))
			}
			select {
			case <-ctx.Done():
				return nil
			case <-watch:
			}
		}
	}))
}
