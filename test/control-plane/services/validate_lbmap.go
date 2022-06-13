// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package services

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"

	lb "github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/testutils/mockmaps"
	"github.com/pmezard/go-difflib/difflib"
	"golang.org/x/exp/constraints"
)

type goldenLBMapValidator struct {
	expectedFile string
	update       bool
}

func newGoldenLBMapValidator(eventsFile string, update bool) goldenLBMapValidator {
	var v goldenLBMapValidator
	var stepNum int
	fmt.Sscanf(path.Base(eventsFile), "events%d.yaml", &stepNum)
	v.expectedFile = path.Join(path.Dir(eventsFile), fmt.Sprintf("lbmap%d.golden", stepNum))
	v.update = update
	return v
}

func (v goldenLBMapValidator) diffStrings(expected, actual string) (string, bool) {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(expected),
		B:        difflib.SplitLines(actual),
		FromFile: v.expectedFile,
		ToFile:   "<actual>",
		Context:  10,
	}
	out, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return err.Error(), false
	}
	if out != "" {
		return out, false
	}
	return "", true
}

func (v goldenLBMapValidator) validate(t *testing.T, lbmap *mockmaps.LBMockMap) {
	writeLBMap := func() {
		f, err := os.OpenFile(v.expectedFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatal(err)
		}
		writeLBMapAsTable(f, lbmap)
		f.Close()
	}

	if _, err := os.Stat(v.expectedFile); err == nil {
		bs, err := os.ReadFile(v.expectedFile)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		writeLBMapAsTable(&buf, lbmap)
		if diff, ok := v.diffStrings(string(bs), buf.String()); !ok {
			if v.update {
				t.Logf("lbmap mismatch:\n%s", diff)
				t.Logf("updated %s as requested", v.expectedFile)
				writeLBMap()
			} else {
				t.Fatalf("lbmap mismatch:\n%s", diff)
			}
		}
	} else {
		// Mark failed as the expected output was missing, but
		// continue with the rest of the steps.
		t.Fail()
		t.Logf("%s missing, creating...", v.expectedFile)
		writeLBMap()
	}
}

func ipLess(a, b net.IP) bool {
	nipA, _ := netip.AddrFromSlice(a)
	nipB, _ := netip.AddrFromSlice(b)
	return nipA.Compare(nipB) < 0
}

func l3n4AddrLess(a, b *lb.L3n4Addr) bool {
	if a.Protocol < b.Protocol {
		return true
	} else if a.Protocol > b.Protocol {
		return false
	}
	if ipLess(a.IP, b.IP) {
		return true
	}
	return a.Port < b.Port
}

func writeLBMapAsTable(w io.Writer, lbmap *mockmaps.LBMockMap) {
	// Since the order in which backends and services (and their ids)
	// are allocated is non-deterministic, we sort the backends and services
	// by address, and use the new ordering to allocate deterministic ids.
	backends := make([]*lb.Backend, 0, len(lbmap.BackendByID))
	for _, be := range lbmap.BackendByID {
		backends = append(backends, be)
	}
	sort.Slice(backends, func(i, j int) bool {
		return backends[i].L3n4Addr.String() < backends[j].String()
	})
	newBackendIds := map[lb.BackendID]int{}
	for i, be := range backends {
		newBackendIds[be.ID] = i
	}

	services := make([]*lb.SVC, 0, len(lbmap.ServiceByID))
	for _, svc := range lbmap.ServiceByID {
		services = append(services, svc)
	}
	sort.Slice(services, func(i, j int) bool {
		return services[i].Frontend.L3n4Addr.String() < services[j].Frontend.L3n4Addr.String()
	})

	tw := newTableWriter(w, "Services", "ID", "Type", "Frontend", "Backend IDs")
	for i, svc := range services {
		tw.AddRow(
			strconv.FormatInt(int64(i), 10),
			string(svc.Type),
			svc.Frontend.String(),
			showBackendIDs(newBackendIds, svc.Backends),
		)
	}
	tw.Flush()

	tw = newTableWriter(w, "Backends", "ID", "L3n4Addr", "State", "Restored")
	for i, be := range backends {
		stateStr, err := be.State.String()
		if err != nil {
			stateStr = err.Error()
		}

		tw.AddRow(
			strconv.FormatInt(int64(i), 10),
			be.L3n4Addr.String(),
			stateStr,
			fmt.Sprintf("%v", be.RestoredFromDatapath))
	}
	tw.Flush()

}

func showBackendIDs(idMap map[lb.BackendID]int, bes []lb.Backend) string {
	var ids []int
	for _, be := range bes {
		ids = append(ids, idMap[be.ID])
	}
	sort.Ints(ids)
	var strs []string
	for _, id := range ids {
		strs = append(strs, strconv.FormatInt(int64(id), 10))
	}
	return strings.Join(strs, ", ")
}

type orderedComparable interface {
	comparable
	constraints.Ordered
}

// MapInOrder does an in-order traversal of a map.
func MapInOrder[K orderedComparable, V any](m map[K]V, fn func(K, V)) {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	for _, k := range keys {
		fn(k, m[k])
	}
}
