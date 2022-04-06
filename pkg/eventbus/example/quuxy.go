package main

import (
	"github.com/cilium/cilium/pkg/eventbus"
	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
)

var (
	logQ = logging.DefaultLogger.WithField(logfields.LogSubsys, "quuxy")
)

//
// Events emitted by Quuxy
//

// Ready event
type QuuxyEventReady struct{}

func (e *QuuxyEventReady) String() string { return "Ready" }

var QuuxyEventReadyP = eventbus.NewEventPrototype("Quuxy subsystem is ready", &QuuxyEventReady{})

//
// Implementation
//

type Quuxy struct {
	events *eventbus.EventPublisher
}

func NewQuuxy(bus *eventbus.EventBus, foob *Foob) (*Quuxy, error) {
	quuxy := &Quuxy{}
	var err error
	quuxy.events, err = bus.RegisterSubsystem(
		quuxy,
		[]eventbus.EventPrototype{QuuxyEventReadyP},
		[]eventbus.EventPrototype{FoobEventFooP},
	)
	if err != nil {
		return nil, err
	}
	return quuxy, nil
}

func (quuxy *Quuxy) SysName() string { return "quuxy" }

func (quuxy *Quuxy) SysEventHandler(ev eventbus.Event) error {
	logQ.Infof("Received event: %s", ev)
	return nil
}
