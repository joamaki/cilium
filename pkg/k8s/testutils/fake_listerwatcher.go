// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package testutils

import (
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
)

// FakeListerWatcher implements a fake cache.ListerWatcher that can be fed
// objects directly, or from YAML files or strings. The YAML data is decoded
// with a schema that covers the Cilium CRDs and Slim schemas.
type FakeListerWatcher struct {
	watcher *watch.FakeWatcher
	added   sets.Set[types.NamespacedName]
}

func NewFakeListerWatcher() *FakeListerWatcher {
	return &FakeListerWatcher{
		watcher: watch.NewFake(),
		added:   sets.New[types.NamespacedName](),
	}
}

func getNamespacedName(obj runtime.Object) types.NamespacedName {
	m, err := meta.Accessor(obj)
	if err != nil {
		panic(err)
	}
	return types.NamespacedName{Namespace: m.GetNamespace(), Name: m.GetName()}
}

func (f *FakeListerWatcher) Upsert(obj runtime.Object) {
	name := getNamespacedName(obj)
	if f.added.Has(name) {
		f.watcher.Modify(obj)
	} else {
		f.watcher.Add(obj)
		f.added.Insert(name)
	}
}

func (f *FakeListerWatcher) Delete(obj runtime.Object) {
	f.watcher.Delete(obj)
	f.added.Delete(getNamespacedName(obj))
}

func (f *FakeListerWatcher) Stop() {
	f.watcher.Stop()
}

func (f *FakeListerWatcher) UpsertFromFile(path string) error {
	if obj, err := DecodeFile(path); err == nil {
		f.Upsert(obj)
		return nil
	} else {
		return err
	}
}

func (f *FakeListerWatcher) DeleteFromFile(path string) error {
	if obj, err := DecodeFile(path); err == nil {
		f.Delete(obj)
		return nil
	} else {
		return err
	}
}

func (f *FakeListerWatcher) UpsertFromText(content string) error {
	if obj, err := DecodeObject([]byte(content)); err == nil {
		f.Upsert(obj)
		return nil
	} else {
		return err
	}
}

func (f *FakeListerWatcher) DeleteFromText(content string) error {
	if obj, err := DecodeObject([]byte(content)); err == nil {
		f.Delete(obj)
		return nil
	} else {
		return err
	}
}

var emptyList = &v1.PartialObjectMetadataList{}

// List implements cache.ListerWatcher.
func (f *FakeListerWatcher) List(options v1.ListOptions) (runtime.Object, error) {
	return emptyList, nil

}

// Watch implements cache.ListerWatcher.
func (f *FakeListerWatcher) Watch(options v1.ListOptions) (watch.Interface, error) {
	return f.watcher, nil
}
