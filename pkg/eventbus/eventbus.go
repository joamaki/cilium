// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package eventbus

import (
	"fmt"
	"sync"
)

// EventBus provides a publish-subscribe service for broadcasting events from a subsystem.
type EventBus struct {
	sync.RWMutex

	pubs   map[eventTypeId]Subsystem
	subs   map[eventTypeId][]Subsystem
	protos map[eventTypeId]EventPrototype
}

func NewEventBus() *EventBus {
	return &EventBus{
		pubs:   map[eventTypeId]Subsystem{},
		subs:   map[eventTypeId][]Subsystem{},
		protos: map[eventTypeId]EventPrototype{},
	}
}

func (bus *EventBus) DumpGraph() {
	bus.RLock()
	defer bus.RUnlock()

	// Construct the reverse of 'subs', e.g. map from subsystem to event types
	revSubs := map[Subsystem][]eventTypeId{}
	for typeId, ss := range bus.subs {
		for _, subsys := range ss {
			revSubs[subsys] = append(revSubs[subsys], typeId)
		}
	}

	for subsys, typs := range revSubs {
		fmt.Printf("%s:\n", subsys.SysName())
		for _, typ := range typs {
			fmt.Printf("\t%s\n", bus.protos[typ])
		}
	}
}

type Subsystem interface {
	// SysName returns the subsystem name.
	SysName() string

	// EventHandler is the method called to handle any of the events
	// listed by Subscribes.
	SysEventHandler(ev Event) error

	// TODO since registration happens in dependency order systems
	// that emit events are started before the systems consuming them.
	// We can either buffer the events, or we can structure this in
	// two phases, first subsystem registers itself and then it's started
	// when all subsystems have been registered.
	//SysStart() error
}

// RegisterSubsystem registers a subsystem to the event bus. By registering the
// subsystem is given a handle that allows the subsystem to publish events.
// A subsystem can only register for events known to the event bus and returns
// an error if an event listed by Subscribes has not been registered.
func (bus *EventBus) RegisterSubsystem(subsys Subsystem, publishes, subscribes []EventPrototype) (*EventBusHandle, error) {
	bus.Lock()
	defer bus.Unlock()
	// TODO rewind of changes on errors

	// Register event types published by this subsystem.
	for _, proto := range publishes {
		if subsys2, ok := bus.pubs[proto.typeId]; ok {
			return nil, fmt.Errorf("event type %q already registered by %s", proto.typeId, subsys2.SysName())
		}
		fmt.Printf("Registered event type %q (id: %d)\n", proto.typeName, proto.typeId)
		bus.pubs[proto.typeId] = subsys
		bus.protos[proto.typeId] = proto
	}

	for _, proto := range subscribes {
		// Validate that some subsystem already publishes this event.
		if _, ok := bus.pubs[proto.typeId]; !ok {
			return nil, fmt.Errorf("event type %q is not known to the event bus", proto.typeId)
		}
		bus.subs[proto.typeId] = append(bus.subs[proto.typeId], subsys)
	}

	return &EventBusHandle{bus, subsys}, nil
}

func (bus *EventBus) Close() error {
	panic("TODO CLOSE")
}

type EventBusHandle struct {
	bus    *EventBus
	subsys Subsystem
}

// Publish an event. The method will fail if the subsystem has not declared
// the event type when it registered. Subscribers are invoked sequentially
// in the order they've registered.
func (h *EventBusHandle) Publish(ev Event) error {
	h.bus.RLock()
	defer h.bus.RUnlock()

	typeId := eventToTypeId(ev)

	regSubsys, ok := h.bus.pubs[typeId]
	if !ok {
		return fmt.Errorf("event type %q not registered", typeId)
	}
	if regSubsys != h.subsys {
		return fmt.Errorf("event type %q registered to another subsystem: %s", typeId, regSubsys.SysName())
	}

	subs, ok := h.bus.subs[typeId]
	if !ok || len(subs) == 0 {
		return fmt.Errorf("no subscribers to %s", h.bus.protos[typeId])
	}

	// TODO: Holding RLock and calling subscribers. Consider dropping locking and only allowing
	// registering before event bus has been started?

	for _, subsys := range subs {
		subsys.SysEventHandler(ev)
		// TODO: ^ short-circuit and return error to publishing subsystem?
		// ... or what do we want to do here?
		// TODO: event handling metrics
	}

	return nil
}

// Unregister the subsystem from the registry.
func (h *EventBusHandle) Unregister() error {
	panic("TODO Unregister")
}
