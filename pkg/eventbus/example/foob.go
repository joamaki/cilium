package main

import (
	"context"
	"sync"
	"time"

	"github.com/cilium/cilium/pkg/eventbus"
	"go.uber.org/fx"
)

//
// Events emitted by Foob
//

type FoobEventFoo struct {
	Timestamp time.Time
}

func (e *FoobEventFoo) String() string { return "Foo has happened" }

//
// Implementation
//

type Foob struct {
	publishFoo eventbus.PublishFunc
	wg         sync.WaitGroup
}

func NewFoob(lc fx.Lifecycle, builder *eventbus.Builder) (*Foob, error) {
	foob := &Foob{}
	var err error
	foob.publishFoo, err = builder.Register(
		new(FoobEventFoo),
		"Foob",
	)
	if err != nil {
		return nil, err
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			foob.wg.Add(1)
			go foob.worker()
			return nil
		},
		OnStop: func(context.Context) error {
			foob.wg.Wait()
			return nil
		},
	})

	return foob, nil
}

func (foob *Foob) worker() {
	time.Sleep(time.Second)
	foob.publishFoo(&FoobEventFoo{time.Now()})
	foob.wg.Done()
}
