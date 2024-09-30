// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package testutils

import (
	"errors"

	"github.com/rogpeppe/go-internal/testscript"
	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/cilium/cilium/pkg/k8s/client"
	"github.com/cilium/cilium/pkg/k8s/version"
)

const tsFakeClientKey = "k8s_fake_client"

func SetupK8sCommand(e *testscript.Env, fc *client.FakeClientset) {
	version.Force("1.24.1")
	e.Values[tsFakeClientKey] = fc

}

func K8sCommand(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) < 1 {
		ts.Fatalf("usage: k8s <command> files...\n<command> is one of add, update or delete.")
	}

	action := args[0]
	if len(args) < 2 {
		ts.Fatalf("usage: k8s %s files...", action)
	}

	for _, file := range args[1:] {
		b := ts.ReadFile(file)
		obj, gvk, err := DecodeObjectGVK([]byte(b))
		if err != nil {
			ts.Fatalf("Decoding %s failed: %s", args[0], err)
		}
		gvr, _ := meta.UnsafeGuessKindToResource(*gvk)
		objMeta, err := meta.Accessor(obj)
		if err != nil {
			ts.Fatalf("Accessor failed on %T: %s", obj, err)
		}
		name := objMeta.GetName()
		ns := objMeta.GetNamespace()

		// Try to add the object to all the trackers. If one of them
		// accepts we're good. We'll add to all since multiple trackers
		// may accept (e.g. slim and kubernetes).
		fc := ts.Value(tsFakeClientKey).(*client.FakeClientset)

		// err will get set to nil if any of the tracker methods succeed.
		// start with a non-nil default error.
		err = errors.New("fail")
		checkErr := func(e error) {
			if err != nil {
				err = e
			}
		}
		switch action {
		case "add":
			checkErr(fc.SlimFakeClientset.Tracker().Add(obj))
			checkErr(fc.CiliumFakeClientset.Tracker().Add(obj))
			checkErr(fc.APIExtFakeClientset.Tracker().Add(obj))
			checkErr(fc.MCSAPIFakeClientset.Tracker().Add(obj))
			checkErr(fc.KubernetesFakeClientset.Tracker().Add(obj))
		case "update":
			checkErr(fc.SlimFakeClientset.Tracker().Update(gvr, obj, ns))
			checkErr(fc.CiliumFakeClientset.Tracker().Update(gvr, obj, ns))
			checkErr(fc.APIExtFakeClientset.Tracker().Update(gvr, obj, ns))
			checkErr(fc.MCSAPIFakeClientset.Tracker().Update(gvr, obj, ns))
			checkErr(fc.KubernetesFakeClientset.Tracker().Update(gvr, obj, ns))
		case "delete":
			checkErr(fc.SlimFakeClientset.Tracker().Delete(gvr, ns, name))
			checkErr(fc.CiliumFakeClientset.Tracker().Delete(gvr, ns, name))
			checkErr(fc.APIExtFakeClientset.Tracker().Delete(gvr, ns, name))
			checkErr(fc.MCSAPIFakeClientset.Tracker().Delete(gvr, ns, name))
			checkErr(fc.KubernetesFakeClientset.Tracker().Delete(gvr, ns, name))
		}

		if err != nil {
			ts.Fatalf("None of the trackers of FakeClientset accepted %T: %s", obj, err)
		}
	}
}
