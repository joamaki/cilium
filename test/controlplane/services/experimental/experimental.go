// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package nodeport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"testing"
	"text/tabwriter"

	"github.com/cilium/hive/cell"
	"github.com/cilium/statedb"

	loadbalancer_experimental "github.com/cilium/cilium/pkg/loadbalancer/experimental"
	agentOption "github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/test/controlplane"
	"github.com/cilium/cilium/test/controlplane/services/helpers"
	"github.com/cilium/cilium/test/controlplane/suite"
)

type helper struct {
	db   *statedb.DB
	svcs *loadbalancer_experimental.Services
}

func newHelper(db *statedb.DB, svcs *loadbalancer_experimental.Services) *helper {
	return &helper{db, svcs}

}

func init() {
	suite.AddTestCase("Services/Experimental", func(t *testing.T) {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}

		modConfig := func(cfg *agentOption.DaemonConfig) {
			cfg.EnableNodePort = true
		}

		versions := controlplane.K8sVersions()
		version := versions[len(versions)-1]

		abs := func(f string) string { return path.Join(cwd, "services", "experimental", "v"+version, f) }

		var h *helper
		grabHelper := cell.Group(
			cell.Provide(newHelper),
			cell.Invoke(func(h_ *helper) {
				h = h_
			}),
		)

		// Run the test from the perspective of "experimental-worker"
		nodeName := "experimental-worker"
		t.Run("v"+version+"/"+nodeName, func(t *testing.T) {
			test := suite.NewControlPlaneTest(t, nodeName, version)

			// Feed in initial state and start the agent.
			test.
				UpdateObjectsFromFile(abs("init.yaml")).
				SetupEnvironment().
				SetCommandLineArgs("--enable-new-services").
				StartAgent(modConfig, grabHelper).
				EnsureWatchers("endpointslices", "pods", "services").
				UpdateObjectsFromFile(abs("state1.yaml")).
				Eventually(func() error { return validate(test, h, abs("expected1_"+nodeName)) }).
				UpdateObjectsFromFile(abs("state2.yaml")).
				Eventually(func() error { return validate(test, h, abs("expected2_"+nodeName)) }).
				// Finally delete everything that was created and check that cleanup works.
				DeleteObjectsFromFile(abs("state1.yaml")).
				Eventually(func() error { return validate(test, h, abs("expected3_"+nodeName)) }).
				StopAgent().
				ClearEnvironment()
		})
	})
}

func validate(test *suite.ControlPlaneTest, h *helper, filePrefix string) error {
	var errs []error

	// Validate the contents of the fake LBMap
	if err := helpers.ValidateLBMapGoldenFile(filePrefix+"_lbmap.golden", test.Datapath); err != nil {
		errs = append(errs, err)
	}

	// Validate the contents of the frontend and backend tables.
	var buf bytes.Buffer
	dumpServices(h, &buf)
	if err := helpers.ValidateOutput(filePrefix+"_tables.golden", buf.String()); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func dumpServices(h *helper, to io.Writer) {
	txn := h.db.ReadTxn()

	// Custom table dumping for services that doesn't include non-deterministic data.
	w := tabwriter.NewWriter(to, 5, 0, 3, ' ', 0)

	fmt.Fprintln(w, "--- Frontends ---")
	fmt.Fprintln(w, "Name\tID non-zero\tAddress\tType\tSource\tStatusKind")
	iter, _ := h.svcs.Frontends().All(txn)
	for svc, _, ok := iter.Next(); ok; svc, _, ok = iter.Next() {
		fmt.Fprintf(w, "%s\t%v\t%s\t%s\t%s\t%s\n",
			svc.Name.String(),
			svc.ID != 0,
			svc.Address.StringWithProtocol(),
			svc.Type,
			svc.Source,
			svc.Status.Kind,
		)
	}

	fmt.Fprintln(w, "--- Backends ---")
	fmt.Fprintln(w, "Address\tSource\tState\tReferencedBy")
	iterBe, _ := h.svcs.Backends().All(txn)
	for be, _, ok := iterBe.Next(); ok; be, _, ok = iterBe.Next() {
		fmt.Fprintf(w, "%s\t%s\t%d\t%v\n",
			be.L3n4Addr.StringWithProtocol(),
			be.Source,
			be.ActualState,
			concatStringer(be.ReferencedBy.AsSlice()),
		)
	}

	w.Flush()
}

func concatStringer[T fmt.Stringer](xs []T) string {
	out := make([]string, len(xs))
	for i := range xs {
		out[i] = xs[i].String()
	}
	return strings.Join(out, ", ")
}
