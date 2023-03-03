// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package main

import (
	"context"
	"path"

	"github.com/cilium/workerpool"
	"github.com/sirupsen/logrus"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	identityCache "github.com/cilium/cilium/pkg/identity/cache"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/k8s/resource"
	"github.com/cilium/cilium/pkg/kvstore"
)

var identitySynchronizerCell = cell.Invoke(registerIdentitySynchronizer)

func registerIdentitySynchronizer(lc hive.Lifecycle, backend kvstore.BackendOperations, identities resource.Resource[*ciliumv2.CiliumIdentity]) {
	is := &identitySynchronizer{backend: backend, identities: identities}
	lc.Append(is)
}

func updateIdentity(backend kvstore.BackendOperations, identity *ciliumv2.CiliumIdentity) error {
	if identity == nil || identity.SecurityLabels == nil {
		log.Warningf("Ignoring invalid identity %+v", identity)
		return nil
	}

	keyPath := path.Join(identityCache.IdentitiesPath, "id", identity.Name)
	labelArray := parseLabelArrayFromMap(identity.SecurityLabels)

	var key []byte
	for _, l := range labelArray {
		key = append(key, l.FormatForKVStore()...)
	}

	if len(key) == 0 {
		return nil
	}

	keyEncoded := []byte(kvstore.Client().Encode(key))
	log.WithFields(logrus.Fields{"key": keyPath, "value": string(keyEncoded)}).Info("Updating identity in etcd")

	_, err := backend.UpdateIfDifferent(context.Background(), keyPath, keyEncoded, true)
	if err != nil {
		log.WithError(err).Warningf("Unable to update identity %s in etcd", keyPath)
	}
	return err
}

func deleteIdentity(backend kvstore.BackendOperations, identity *ciliumv2.CiliumIdentity) error {
	if identity == nil {
		log.Warningf("Igoring invalid identity %+v", identity)
		return nil
	}

	keyPath := path.Join(identityCache.IdentitiesPath, "id", identity.Name)
	err := backend.Delete(context.Background(), keyPath)
	if err != nil {
		log.WithError(err).Warningf("Unable to delete identity %s in etcd", keyPath)
	}
	return err
}

type identitySynchronizer struct {
	wp         *workerpool.WorkerPool
	backend    kvstore.BackendOperations
	identities resource.Resource[*ciliumv2.CiliumIdentity]
}

func (is *identitySynchronizer) eventLoop(ctx context.Context) error {
	for ev := range is.identities.Events(ctx) {
		switch ev.Kind {
		case resource.Upsert:
			ev.Done(updateIdentity(is.backend, ev.Object))
		case resource.Delete:
			ev.Done(deleteIdentity(is.backend, ev.Object))
		default:
			ev.Done(nil)
		}
	}
	return nil
}

func (is *identitySynchronizer) Start(hive.HookContext) error {
	is.wp = workerpool.New(1)
	return is.wp.Submit("eventLoop", is.eventLoop)
}

func (is *identitySynchronizer) Stop(hive.HookContext) error {
	if is.wp != nil {
		return is.wp.Close()
	}
	return nil
}
