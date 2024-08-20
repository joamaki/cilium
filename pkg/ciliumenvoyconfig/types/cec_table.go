// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package types

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cilium/statedb"
	"github.com/cilium/statedb/index"
	"github.com/cilium/statedb/part"
	"github.com/cilium/statedb/reconciler"
	k8sTypes "k8s.io/apimachinery/pkg/types"

	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/labels"
	"github.com/cilium/cilium/pkg/loadbalancer"
)

type CEC struct {
	Name k8sTypes.NamespacedName
	Spec *ciliumv2.CiliumEnvoyConfigSpec

	Selector         labels.Selector `json:"-"`
	SelectsLocalNode bool
	Listeners        part.Map[string, uint16]

	// Resources is the parsed envoy.Resources. It is 'any' typed
	// here to keep the footprint small. If we would have 'envoy.Resources'
	// here it would bloat cilium-dbg by 20MB.
	Resources any `json:"-"`

	// ReconciledResources is the last successfully reconciled resources.
	// Updated by the reconciliation operations.
	ReconciledResources any `json:"-"`

	Status reconciler.Status
}

func (cec *CEC) Clone() *CEC {
	cec2 := *cec
	return &cec2
}

func (cec *CEC) SetStatus(newStatus reconciler.Status) *CEC {
	cec.Status = newStatus
	return cec
}

func (cec *CEC) GetStatus() reconciler.Status {
	return cec.Status
}

func (*CEC) TableHeader() []string {
	return []string{
		"Name",
		"Selected",
		"NodeSelector",
		"Services",
		"BackendServices",
		"Listeners",
		"Status",
	}
}

func (cec *CEC) TableRow() []string {
	var services, beServices, listeners []string
	for _, svcl := range cec.Spec.Services {
		services = append(services, svcl.Namespace+"/"+svcl.Name)
	}
	for _, svcl := range cec.Spec.BackendServices {
		beServices = append(beServices, svcl.Namespace+"/"+svcl.Name)
	}
	for name, port := range cec.Listeners.All() {
		listeners = append(listeners, fmt.Sprintf("%s:%d", name, port))
	}
	return []string{
		cec.Name.String(),
		strconv.FormatBool(cec.SelectsLocalNode),
		cec.Selector.String(),
		strings.Join(services, ", "),
		strings.Join(beServices, ", "),
		strings.Join(listeners, ", "),
		cec.Status.String(),
	}
}

var (
	CECTableName = "ciliumenvoyconfigs"

	cecNameIndex = statedb.Index[*CEC, k8sTypes.NamespacedName]{
		Name: "name",
		FromObject: func(obj *CEC) index.KeySet {
			return index.NewKeySet(index.String(obj.Name.String()))
		},
		FromKey: index.Stringer[k8sTypes.NamespacedName],
		Unique:  true,
	}

	CECByName = cecNameIndex.Query

	cecServiceIndex = statedb.Index[*CEC, loadbalancer.ServiceName]{
		Name: "service",
		FromObject: func(obj *CEC) index.KeySet {
			keys := make([]index.Key, len(obj.Spec.Services))
			for i, svcl := range obj.Spec.Services {
				keys[i] = index.String(
					loadbalancer.ServiceName{
						Namespace: svcl.Namespace,
						Name:      svcl.Name,
					}.String(),
				)
			}
			return index.NewKeySet(keys...)
		},
		FromKey: func(key loadbalancer.ServiceName) index.Key {
			return index.String(key.String())
		},
		Unique: false,
	}

	CECByServiceName = cecServiceIndex.Query
)

func NewCECTable(db *statedb.DB) (statedb.RWTable[*CEC], error) {
	tbl, err := statedb.NewTable(
		CECTableName,
		cecNameIndex,
		cecServiceIndex,
	)
	if err != nil {
		return nil, err
	}
	return tbl, db.RegisterTable(tbl)
}
