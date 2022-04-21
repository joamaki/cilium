// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

// Package pinit is a utility for performing parallel initialization.
// Modules register their parallel initialization functions along with
// the checkpoint at which it can start executing. All functions registered
// to a specific checkpoint must have finished executing before functions
// from the next checkpoint are invoked.
package pinit

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"go.uber.org/fx"
)

var Module = fx.Module(
	"parallel init",
	fx.Provide(NewParInit),
)

var log = logging.DefaultLogger.WithField(logfields.LogSubsys, "pinit")

type InitFunc func() error

type Checkpoint int

const (
	C0 = iota
	C1
	C2
	C3
	C4
)

type Registerer interface {
	Register(cp Checkpoint, fn InitFunc)
}

type annInitFunc struct {
	desc string
	from string
	fn   InitFunc
}

func (afn *annInitFunc) String() string {
	return fmt.Sprintf("%s <%s>", afn.desc, afn.from)
}

type ParInit struct {
	sync.Mutex
	initFuncs [5][]annInitFunc
}

func NewParInit(lc fx.Lifecycle) *ParInit {
	pi := &ParInit{}
	lc.Append(fx.Hook{
		OnStart: pi.Execute,
	})
	return pi
}

func (p *ParInit) Register(cp Checkpoint, desc string, fn InitFunc) {
	p.Lock()
	defer p.Unlock()

	from := "<unknown>"
	if pc, _, lineNum, ok := runtime.Caller(1); ok {
		if fn := runtime.FuncForPC(pc); fn != nil {
			from = fmt.Sprintf("%s#%d", fn.Name(), lineNum)
		}
	}
	p.initFuncs[cp] = append(p.initFuncs[cp], annInitFunc{desc, from, fn})
}

func (p *ParInit) Execute(ctx context.Context) error {
	p.Lock()
	defer p.Unlock()

	p.logInitFuncs()

	for cp := C0; cp <= C4; cp++ {
		fns := p.initFuncs[cp]
		errs := make(chan error, len(fns))
		defer close(errs)

		for _, afn := range fns {
			go func(afn annInitFunc) {
				errs <- afn.fn()
			}(afn)
		}

		for range fns {
			select {
			case <-ctx.Done():
				return ctx.Err()

			case err := <-errs:
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (p *ParInit) logInitFuncs() {
	log.Debug("Parallel initialization functions:")

	for cp := C0; cp <= C4; cp++ {
		log.WithField("funcs", p.initFuncs[cp]).
			Debugf("Checkpoint %d", cp)
	}
}
