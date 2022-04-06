package main

import (
	"context"
	"sync"
	"time"

	"github.com/cilium/cilium/pkg/eventbus"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"go.uber.org/fx"
)

var (
	log = logging.DefaultLogger.WithField(logfields.LogSubsys, "foob")
)

//
// Events emitted by Foob
//

// Ready event
type FoobEventReady struct{}

func (e *FoobEventReady) String() string { return "Ready" }

var FoobEventReadyP = eventbus.NewEventPrototype("Foob subsystem is ready", &FoobEventReady{})

// Foo event

type FoobEventFoo struct {
	Timestamp time.Time
}

func (e *FoobEventFoo) String() string { return "Foo has happened" }

var FoobEventFooP = eventbus.NewEventPrototype("The dreaded Foo event has happened", &FoobEventFoo{})

//
// Implementation
//

type Foob struct {
	events *eventbus.EventPublisher
	wg     sync.WaitGroup
}

func NewFoob(lc fx.Lifecycle, bus *eventbus.EventBus) (*Foob, error) {
	foob := &Foob{}
	var err error
	foob.events, err = bus.RegisterSubsystem(
		foob,
		[]eventbus.EventPrototype{FoobEventReadyP, FoobEventFooP},
		nil,
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
	foob.events.Publish(&FoobEventFoo{time.Now()})
	foob.wg.Done()
}

func (foob *Foob) SysName() string { return "foob" }

func (foob *Foob) SysEventHandler(ev eventbus.Event) error {
	log.Infof("Received event: %s", ev)
	return nil
}
