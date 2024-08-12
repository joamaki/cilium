// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package testutils

import (
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	slim_fake "github.com/cilium/cilium/pkg/k8s/slim/k8s/client/clientset/versioned/fake"
)

// Decoder is an object decoder for Cilium and Slim objects.
// The [DecodeObject] and [DecodeFile] functions are provided as
// shorthands for decoding from bytes and files respectively.
var Decoder runtime.Decoder

func init() {
	scheme := runtime.NewScheme()
	slim_fake.AddToScheme(scheme)
	cilium_v2.AddToScheme(scheme)
	Decoder = serializer.NewCodecFactory(scheme).UniversalDeserializer()
}

func DecodeObject(bytes []byte) (runtime.Object, error) {
	obj, _, err := Decoder.Decode(bytes, nil, nil)
	return obj, err
}

func DecodeFile(path string) (runtime.Object, error) {
	bs, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeObject(bs)
}
