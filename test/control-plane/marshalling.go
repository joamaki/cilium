// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package controlplane

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	fakeApiExt "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/fake"

	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
	slim_fake "github.com/cilium/cilium/pkg/k8s/slim/k8s/client/clientset/versioned/fake"
)

//
// Marshalling utilities for control-plane tests.
//

var (
	// coreDecoder decodes objects using only the corev1 scheme
	coreDecoder = newCoreSchemeDecoder()

	apiextDecoder = newApiextSchemeDecoder()

	// slimDecoder decodes objects with the slim scheme
	slimDecoder = newSlimSchemeDecoder()

	// ciliumDecoder decodes objects with the cilium v2 scheme
	ciliumDecoder = newCiliumSchemeDecoder()

	allSchemes = k8sRuntime.NewScheme()

	core    k8sRuntime.Decoder
	decoder k8sRuntime.Decoder
)

func init() {
	slim_fake.AddToScheme(allSchemes)
	allSchemes.AddKnownTypes(slim_corev1.SchemeGroupVersion, &metav1.List{})
	cilium_v2.AddToScheme(allSchemes)

	//fake.AddToScheme(allSchemes)

	cf := serializer.NewCodecFactory(allSchemes)
	decoder = cf.UniversalDecoder(
		corev1.SchemeGroupVersion,
		slim_corev1.SchemeGroupVersion,
		cilium_v2.SchemeGroupVersion,
	)

	coreScheme := k8sRuntime.NewScheme()
	fake.AddToScheme(coreScheme)
	core = serializer.NewCodecFactory(coreScheme).UniversalDecoder(corev1.SchemeGroupVersion)
}

type schemeDecoder struct {
	*k8sRuntime.Scheme
	codecFactory serializer.CodecFactory
}

// known returns true if the object kind is known to the scheme,
// e.g. it can decode it.
func (d schemeDecoder) known(obj k8sRuntime.Object) bool {
	gvk := obj.GetObjectKind().GroupVersionKind()
	return d.Scheme.Recognizes(gvk)
}

// convert converts the input object (usually Unstructured) using
// the scheme.
func (d schemeDecoder) convert(obj k8sRuntime.Object) k8sRuntime.Object {
	_, _, err := d.Scheme.ObjectKinds(obj)
	if err == nil {
		// No need to convert
		return obj
	}

	// Object kind is recognized, but not the type. Convert
	// first to unstructured and then try to convert using the
	// scheme.
	// TODO: Might want to instead go via JSON as some objects
	// cannot be converted (e.g. cilium_v2.EndpointSelectorSlice)
	// as they have unexported fields. Need to go via their custom
	// unmarshallers.
	if _, ok := obj.(*unstructured.Unstructured); !ok {
		// Object is not unstructured. Convert it to one so it can be unmarshalled
		// in different ways, e.g. as v1.Node and slim_v1.Node.
		fields, err := k8sRuntime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			panic("Failed to convert to unstructured")
		}
		obj = &unstructured.Unstructured{Object: fields}
	}
	gvk := obj.GetObjectKind().GroupVersionKind()
	out, err := d.Scheme.ConvertToVersion(obj, gvk.GroupVersion())
	if err != nil {
		panic(err)
	}
	return out
}

func newSlimSchemeDecoder() schemeDecoder {
	s := k8sRuntime.NewScheme()
	slim_fake.AddToScheme(s)
	s.AddKnownTypes(slim_corev1.SchemeGroupVersion, &metav1.List{})
	cf := serializer.NewCodecFactory(s)
	return schemeDecoder{s, cf}
}

func newCiliumSchemeDecoder() schemeDecoder {
	s := k8sRuntime.NewScheme()
	cilium_v2.AddToScheme(s)
	cf := serializer.NewCodecFactory(s)
	return schemeDecoder{s, cf}
}

func newCoreSchemeDecoder() schemeDecoder {
	s := k8sRuntime.NewScheme()
	fake.AddToScheme(s)
	fakeApiExt.AddToScheme(s)
	cf := serializer.NewCodecFactory(s)
	return schemeDecoder{s, cf}
}

func newApiextSchemeDecoder() schemeDecoder {
	s := k8sRuntime.NewScheme()
	fakeApiExt.AddToScheme(s)
	cf := serializer.NewCodecFactory(s)
	return schemeDecoder{s, cf}
}

// unmarshalList unmarshals the input yaml data into an unstructured list.
func unmarshalList(bs []byte) []k8sRuntime.Object {
	var items unstructured.UnstructuredList
	err := yaml.Unmarshal(bs, &items)
	if err != nil {
		panic(err)
	}
	var objs []k8sRuntime.Object
	for _, item := range items.Items {
		bs, err := item.MarshalJSON()
		if err != nil {
			panic(err)
		}
		obj, _, err := decoder.Decode(bs, nil, nil)
		if err != nil {
			panic(err)
		}

		fmt.Printf(">>> Decoded as %T\n", obj)
		objs = append(objs, obj)

		// FIXME: hack as we need to double-decode objects
		// for slim and core.
		obj, _, err = core.Decode(bs, nil, nil)
		if err == nil {
			fmt.Printf(">>> Also decoded as %T\n", obj)
			objs = append(objs, obj)
		}

		return nil
	}
	return objs
}

func MustUnmarshal(s string) k8sRuntime.Object {
	obj, _, err := decoder.Decode([]byte(s), nil, nil)
	if err != nil {
		panic(err)
	}
	return obj
}
