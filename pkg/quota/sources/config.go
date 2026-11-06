// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quotasources

import (
	"fmt"

	"github.com/cilium/statedb"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/cilium/cilium/pkg/quota"
	"github.com/cilium/cilium/pkg/source"
)

const configOrigin = "config"

// Config contains default quota limits configured through agent flags.
type Config struct {
	Limits map[string]string `mapstructure:"default-resource-quotas"`
}

// DefaultConfig contains the default configuration for quota limits.
var DefaultConfig = Config{Limits: map[string]string{}}

// Flags registers the quota configuration flag.
func (cfg Config) Flags(flags *pflag.FlagSet) {
	flags.StringToString("default-resource-quotas", cfg.Limits, "Default resource quotas")
}

// ApplyConfig reflects configured limits into the ResourceQuota table as
// cluster-wide defaults. Namespaced quota sources can override these defaults.
func ApplyConfig(db *statedb.DB, table statedb.RWTable[quota.ResourceQuota], cfg Config) error {
	txn := db.WriteTxn(table)
	defer txn.Abort()

	for name, value := range cfg.Limits {
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			return fmt.Errorf("invalid quota %q value %q: %w", name, value, err)
		}
		limit := quantity.Value()
		if limit < 0 || quantity.Cmp(*resource.NewQuantity(limit, resource.DecimalSI)) != 0 {
			return fmt.Errorf("quota %q value %q must be a non-negative integer quantity", name, value)
		}

		resourceName := quota.ResourceName(name)
		resourceQuota, _, found := table.Get(txn, quota.ResourceQuotaByName("", resourceName))
		if !found {
			resourceQuota = quota.ResourceQuota{
				Name: resourceName,
				Hard: quota.Unlimited,
			}
		}
		resourceQuota.AddInput(quota.ResourceQuotaInput{
			Source: source.LocalAPI,
			Origin: configOrigin,
			Hard:   uint64(limit),
		})
		if _, _, err := table.Insert(txn, resourceQuota); err != nil {
			return fmt.Errorf("adding quota %q from configuration: %w", name, err)
		}
	}

	txn.Commit()
	return nil
}
