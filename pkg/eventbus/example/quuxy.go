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

//
// Implementation
//

type Quuxy struct {
	publishReady eventbus.PublishFunc
	unsubscribe  eventbus.UnsubscribeFunc
}

func NewQuuxy(builder *eventbus.Builder, foob *Foob) (*Quuxy, error) {
	quuxy := &Quuxy{}
	var err error
	quuxy.publishReady, err = builder.Register(
		new(QuuxyEventReady),
		"Quuxy is ready",
	)
	if err != nil {
		return nil, err
	}
	quuxy.unsubscribe = builder.Subscribe(
		new(FoobEventFoo),
		"FoobFoob",
		quuxy.eventHandler,
	)
	return quuxy, nil
}

func (quuxy *Quuxy) eventHandler(ev eventbus.Event) error {
	logQ.Infof("Received event: %s", ev)
	return nil
}
