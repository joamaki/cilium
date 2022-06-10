package services

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"testing"

	gojson "encoding/json"

	fakeDatapath "github.com/cilium/cilium/pkg/datapath/fake"
	"github.com/cilium/cilium/pkg/ipcache"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_discovery_v1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1"
	slim_discovery_v1beta1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/discovery/v1beta1"
	"github.com/cilium/cilium/pkg/k8s/watchers"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/lock"
	"github.com/cilium/cilium/pkg/policy"
	"github.com/cilium/cilium/pkg/policy/api"
	"github.com/cilium/cilium/pkg/service"
	"github.com/cilium/cilium/pkg/testutils/mockmaps"
	"github.com/kr/pretty"
	"github.com/pmezard/go-difflib/difflib"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/yaml"
	yaml2 "sigs.k8s.io/yaml"

	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
)

//
// Test runner for service load-balancing control-plane tests.
//
// Tests in this suite are implemented in terms of verifying
// that the control-plane correctly transforms a list of
// k8s input objects into the correct datapath (lbmap) state.
//
// This approach makes the test cases themselves independent
// of the internal implementation of the control-plane and
// allows using them as-is when control-plane is refactored.
//
// The test cases can be written either as:
//
// - code with manual construction of the k8s objects, steps
//   and validation functions.
//
// - golden test with k8s objects and lbmap state expressed
//   as yaml files.
//

// ValidateFunc is called on each test case step to validate the state
// of the LBMap.
type ValidateFunc func(t *testing.T, lbmap *mockmaps.LBMockMap)

// ServicesTestStep defines a test step, with input objects that are
// fed into the control-plane, and a validation function that is called
// after control-plane has applied the changes.
type ServicesTestStep struct {
	// Desc is the step description
	Desc string

	// Inputs is a slice of k8s objects that the control-plane should apply.
	Inputs []k8sRuntime.Object

	// Validate is called with MockLBMap after 'Inputs' are applied
	Validate ValidateFunc
}

func NewStep(desc string, validate ValidateFunc, inputs ...k8sRuntime.Object) *ServicesTestStep {
	return &ServicesTestStep{desc, inputs, validate}
}

// ServicesTestCase is a collection of test steps for testing the service
// load-balancing of the control-plane.
type ServicesTestCase struct {
	Steps []*ServicesTestStep
}

func NewTestCase(steps ...*ServicesTestStep) *ServicesTestCase {
	return &ServicesTestCase{steps}
}

// Run sets up the control-plane with a mock lbmap and executes the test case
// against it.
func (testCase *ServicesTestCase) Run(t *testing.T) {
	watcher, lbmap := setupTest()
	defer tearDown(watcher)

	// Run through test steps and validate
	for _, step := range testCase.Steps {
		// Feed in the input objects to the service cache
		swg := lock.NewStoppableWaitGroup()
		for _, input := range step.Inputs {
			switch obj := input.(type) {
			case *slim_corev1.Service:
				watcher.K8sSvcCache.UpdateService(obj, swg)
			case *slim_corev1.Endpoints:
				watcher.K8sSvcCache.UpdateEndpoints(obj, swg)
			case *slim_discovery_v1.EndpointSlice:
				watcher.K8sSvcCache.UpdateEndpointSlicesV1(obj, swg)
			case *slim_discovery_v1beta1.EndpointSlice:
				watcher.K8sSvcCache.UpdateEndpointSlicesV1Beta1(obj, swg)
			default:
				t.Fatalf("Invalid test case: input of type %T is unknown", input)
			}
		}
		swg.Stop()
		swg.Wait()

		// Validate LBMap state
		step.Validate(t, lbmap)
	}
}

// goldenLBMapValidator
type goldenLBMapValidator struct {
	expectedFile string
}

func newGoldenLBMapValidator(eventsFile string) goldenLBMapValidator {
	var v goldenLBMapValidator
	var stepNum int
	fmt.Sscanf(path.Base(eventsFile), "events%d.yaml", &stepNum)
	v.expectedFile = path.Join(path.Dir(eventsFile), fmt.Sprintf("lbmap%d.yaml", stepNum))
	return v
}

// lbmapsEqual returns true if the actual content of the lbmap produced by the test
// matches with the expected. If it doesn't, a textual diff is included to describe
// the mismatch.
func (v goldenLBMapValidator) lbmapsEqual(expected, actual *mockmaps.LBMockMap) (string, bool) {
	stringA := pretty.Sprintf("%# v", expected)
	stringB := pretty.Sprintf("%# v", actual)
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(stringA),
		B:        difflib.SplitLines(stringB),
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
	if _, err := os.Stat(v.expectedFile); err == nil {
		var expected mockmaps.LBMockMap
		bs, err := os.ReadFile(v.expectedFile)
		if err != nil {
			t.Fatal(err)
		}
		err = yaml.Unmarshal(bs, &expected)
		if err != nil {
			t.Fatal(err)
		}
		if diff, ok := v.lbmapsEqual(&expected, lbmap); !ok {
			t.Fatalf("lbmap mismatch:\n%s", diff)
		}
	} else {
		// Mark failed as the expected output was missing, but
		// continue with the rest of the steps.
		t.Fail()
		t.Logf("%s missing, creating...", v.expectedFile)
		out, _ := yaml2.Marshal(lbmap)
		if err := os.WriteFile(v.expectedFile, out, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// RunGoldenTest runs the golden test written in terms of YAML files defining
// the input events and expected LBMap output.
//
// The input events are specified in files events1.yaml, events2.yaml and so on.
// These are expected to be a k8s "List", e.g. the format of "kubectl get <res> -o yaml".
//
// The lbmap expected state is stored in lbmap1.yaml, lbmap2.yaml and so on.
// Expected state defined by "lbmap1.yaml" is checked after events in
// "events1.yaml" are applied.
func RunGoldenTest(t *testing.T, dir string) {
	var testCase ServicesTestCase

	// Construct the test case by parsing all input event files
	// (<dir>/events*.yaml)
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Sort the entries alphabetically to process the steps in the expected
	// order.
	sort.Slice(ents, func(i, j int) bool {
		return ents[i].Name() < ents[j].Name()
	})

	for _, ent := range ents {
		if !strings.HasPrefix(ent.Name(), "events") || !strings.HasSuffix(ent.Name(), ".yaml") {
			continue
		}

		eventsFile := path.Join(dir, ent.Name())
		bs, err := os.ReadFile(eventsFile)
		if err != nil {
			t.Fatal(err)
		}

		// Unmarshal the input first into an unstructured list, and then
		// re-Unmarshal each object based on its "kind" using the right
		// unmarshaller.
		var items unstructured.UnstructuredList
		err = yaml.Unmarshal(bs, &items)
		if err != nil {
			t.Fatal(err)
		}
		var objs []k8sRuntime.Object
		items.EachListItem(func(obj k8sRuntime.Object) error {
			objs = append(objs, toStructured(obj))
			return nil
		})

		validator := newGoldenLBMapValidator(eventsFile)
		testCase.Steps = append(testCase.Steps,
			NewStep(path.Join(dir, path.Base(ent.Name())),
				validator.validate,
				objs...))
	}

	testCase.Run(t)
}

// setupTest sets up enough of the control-plane to test the control aspects of
// service load-balancing.
func setupTest() (*watchers.K8sWatcher, *mockmaps.LBMockMap) {
	policyManager := &fakePolicyManager{
		OnTriggerPolicyUpdates: func(force bool, reason string) {
		},
	}
	policyRepository := &fakePolicyRepository{
		OnTranslateRules: func(tr policy.Translator) (result *policy.TranslationResult, e error) {
			return &policy.TranslationResult{NumToServicesRules: 1}, nil
		},
	}

	lbmap := mockmaps.NewLBMockMap()
	svcManager := service.NewServiceWithMap(nil, nil, lbmap)

	w := watchers.NewK8sWatcher(
		nil,
		nil,
		policyManager,
		policyRepository,
		svcManager,
		fakeDatapath.NewDatapath(),
		nil,
		nil,
		nil,
		nil,
		&fakeWatcherConfiguration{},
		ipcache.NewIPCache(nil),
	)
	go w.RunK8sServiceHandler()

	return w, lbmap

}

func tearDown(w *watchers.K8sWatcher) {
	w.StopK8sServiceHandler()
}

// Fakes

type fakeWatcherConfiguration struct{}

func (f *fakeWatcherConfiguration) K8sServiceProxyNameValue() string {
	return ""
}

func (f *fakeWatcherConfiguration) K8sIngressControllerEnabled() bool {
	return false
}

type fakePolicyManager struct {
	OnTriggerPolicyUpdates func(force bool, reason string)
	OnPolicyAdd            func(rules api.Rules, opts *policy.AddOptions) (newRev uint64, err error)
	OnPolicyDelete         func(labels labels.LabelArray) (newRev uint64, err error)
}

func (f *fakePolicyManager) TriggerPolicyUpdates(force bool, reason string) {
	if f.OnTriggerPolicyUpdates != nil {
		f.OnTriggerPolicyUpdates(force, reason)
		return
	}
	panic("OnTriggerPolicyUpdates(force bool, reason string) was called and is not set!")
}

func (f *fakePolicyManager) PolicyAdd(rules api.Rules, opts *policy.AddOptions) (newRev uint64, err error) {
	if f.OnPolicyAdd != nil {
		return f.OnPolicyAdd(rules, opts)
	}
	panic("OnPolicyAdd(api.Rules, *policy.AddOptions) (uint64, error) was called and is not set!")
}

func (f *fakePolicyManager) PolicyDelete(labels labels.LabelArray) (newRev uint64, err error) {
	if f.OnPolicyDelete != nil {
		return f.OnPolicyDelete(labels)
	}
	panic("OnPolicyDelete(labels.LabelArray) (uint64, error) was called and is not set!")
}

type fakePolicyRepository struct {
	OnGetSelectorCache func() *policy.SelectorCache
	OnTranslateRules   func(translator policy.Translator) (*policy.TranslationResult, error)
}

func (f *fakePolicyRepository) GetSelectorCache() *policy.SelectorCache {
	if f.OnGetSelectorCache != nil {
		return f.OnGetSelectorCache()
	}
	panic("OnGetSelectorCache() (*policy.SelectorCache) was called and is not set!")
}

func (f *fakePolicyRepository) TranslateRules(translator policy.Translator) (*policy.TranslationResult, error) {
	if f.OnTranslateRules != nil {
		return f.OnTranslateRules(translator)
	}
	panic("OnTranslateRules(policy.Translator) (*policy.TranslationResult, error) was called and is not set!")
}

//
// Marshalling utils
//

func unmarshal[T k8sRuntime.Object](in []byte) T {
	var obj T
	err := json.Unmarshal(in, &obj)
	if err != nil {
		panic(err)
	}
	return obj
}

func toStructured(obj k8sRuntime.Object) k8sRuntime.Object {
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	bs, err := obj.(gojson.Marshaler).MarshalJSON()
	if err != nil {
		panic(err)
	}
	switch kind {
	case "Service":
		return unmarshal[*slim_corev1.Service](bs)
	case "EndpointSlice":
		return unmarshal[*slim_discovery_v1.EndpointSlice](bs)
	default:
		panic(fmt.Sprintf("unhandled kind: %s", kind))
	}
}
