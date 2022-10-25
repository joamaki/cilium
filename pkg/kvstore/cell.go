package kvstore

import (
	"context"
	"fmt"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/spf13/pflag"
)

type KVStoreConfig struct {
	KVStore    string
	KVStoreOpt map[string]string
}

func (def KVStoreConfig) Flags(flags *pflag.FlagSet) {

}

type KVStoreParams struct {
	cell.In

	ExtraOptions *ExtraOptions `optional:"true"`
	Config       KVStoreConfig
	Shutdowner   hive.Shutdowner
}

var Cell = cell.Module(
	"kvstore",

	cell.Config(KVStoreConfig{}),
	cell.Provide(kvstoreProvider),
)

func kvstoreProvider(p KVStoreParams) (BackendOperations, error) {
	if p.Config.KVStore == "" {
		return nil, nil
	}

	ctx := context.TODO()

	module := getBackend(p.Config.KVStore)
	if module == nil {
		return nil, fmt.Errorf("unknown key-value store type %q. See cilium.link/err-kvstore for details", p.Config.KVStore)
	}

	if err := module.setConfig(p.Config.KVStoreOpt); err != nil {
		return nil, err
	}

	if err := module.setExtraConfig(p.ExtraOptions); err != nil {
		return nil, err
	}

	// FIXME: Split off connecting from newClient so it can be called from the start hook,
	// e.g. `Connect()` backend operation?
	c, errChan := module.newClient(ctx, p.ExtraOptions)

	go func() {
		select {
		case err := <-errChan:
			p.Shutdowner.Shutdown(hive.ShutdownWithError(err))
		case <-ctx.Done():
		}
	}()

	return c, nil
}
