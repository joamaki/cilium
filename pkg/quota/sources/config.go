// This file was not reviewed by a human before submission
// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package quotasources

import (
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/cilium/statedb"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/cilium/cilium/pkg/quota"
	"github.com/cilium/cilium/pkg/source"
)

const configOrigin = "config"

// Config contains quota limits configured through agent flags.
type Config struct {
	Limits map[string]string `mapstructure:"resource-quotas"`
}

// DefaultConfig contains the default configuration for quota limits.
var DefaultConfig = Config{Limits: map[string]string{}}

// Flags registers the quota configuration flag.
func (cfg Config) Flags(flags *pflag.FlagSet) {
	flags.StringToString("resource-quotas", cfg.Limits, "Resource quotas as namespace:resource=limit; use * for namespace defaults")
}

// parseConfigQuota accepts an explicit namespace or the wildcard default.
func parseConfigQuota(key, value string) (namespace string, name quota.ResourceName, hard uint64, err error) {
	selector, resourceName, found := strings.Cut(strings.TrimSpace(key), ":")
	selector, resourceName = strings.TrimSpace(selector), strings.TrimSpace(resourceName)
	if !found || selector == "" || resourceName == "" {
		return "", "", 0, fmt.Errorf("quota %q must use namespace:resource or *:resource", key)
	}
	if selector != "*" {
		if problems := validation.IsDNS1123Label(selector); len(problems) > 0 {
			return "", "", 0, fmt.Errorf("invalid quota namespace %q: %s", selector, strings.Join(problems, ", "))
		}
		namespace = selector
	}
	if problems := validation.IsQualifiedName(resourceName); len(problems) > 0 {
		return "", "", 0, fmt.Errorf("invalid quota resource %q: %s", resourceName, strings.Join(problems, ", "))
	}
	_, parseErr := resource.ParseQuantity(strings.TrimSpace(value))
	if parseErr != nil {
		return "", "", 0, fmt.Errorf("invalid quota %q value %q: %w", key, value, parseErr)
	}
	// Quantity.Value uses int64, and ParseQuantity can round fractions or cap
	// large binary quantities. Evaluate the validated input exactly instead.
	numeric := strings.TrimSpace(value)
	multiplier := new(big.Rat).SetInt64(1)
	for _, unit := range []struct {
		suffix         string
		base, exponent int64
	}{
		{"Ki", 2, 10}, {"Mi", 2, 20}, {"Gi", 2, 30}, {"Ti", 2, 40}, {"Pi", 2, 50}, {"Ei", 2, 60},
		{"n", 10, -9}, {"u", 10, -6}, {"m", 10, -3}, {"k", 10, 3}, {"M", 10, 6},
		{"G", 10, 9}, {"T", 10, 12}, {"P", 10, 15}, {"E", 10, 18},
	} {
		if !strings.HasSuffix(numeric, unit.suffix) {
			continue
		}
		numeric = strings.TrimSuffix(numeric, unit.suffix)
		exponent := unit.exponent
		if exponent < 0 {
			exponent = -exponent
		}
		factor := new(big.Int).Exp(big.NewInt(unit.base), big.NewInt(exponent), nil)
		if unit.exponent < 0 {
			multiplier.SetFrac(big.NewInt(1), factor)
		} else {
			multiplier.SetInt(factor)
		}
		break
	}
	number, ok := new(big.Rat).SetString(numeric)
	if ok {
		number.Mul(number, multiplier)
	}
	if !ok || !number.IsInt() || !number.Num().IsUint64() {
		return "", "", 0, fmt.Errorf("quota %q value %q must be a non-negative integer fitting uint64", key, value)
	}
	return namespace, quota.ResourceName(resourceName), number.Num().Uint64(), nil
}

// ApplyConfig reflects namespace limits and wildcard defaults into the table.
// Namespace-specific inputs override wildcard defaults; Kubernetes inputs keep
// their higher source priority.
func ApplyConfig(db *statedb.DB, table statedb.RWTable[quota.ResourceQuota], cfg Config) error {
	txn := db.WriteTxn(table)
	defer txn.Abort()
	seen := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(cfg.Limits)) {
		namespace, name, hard, err := parseConfigQuota(key, cfg.Limits[key])
		if err != nil {
			return err
		}
		canonical := namespace + ":" + string(name)
		if previous, exists := seen[canonical]; exists {
			return fmt.Errorf("quota keys %q and %q refer to the same namespace and resource", previous, key)
		}
		seen[canonical] = key
		row, _, found := table.Get(txn, quota.ResourceQuotaByName(namespace, name))
		if !found {
			row = quota.ResourceQuota{Namespace: namespace, Name: name, Hard: quota.Unlimited}
		}
		row.AddInput(quota.ResourceQuotaInput{Source: source.LocalAPI, Origin: configOrigin, Hard: hard})
		if _, _, err := table.Insert(txn, row); err != nil {
			return fmt.Errorf("adding quota %q from configuration: %w", key, err)
		}
	}
	txn.Commit()
	return nil
}
