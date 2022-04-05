// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package eventbus

import (
	"fmt"
	"reflect"
)

// EventBus provides a publish-subscribe service for broadcasting events from a subsystem.
type EventBus struct {
	pubs map[eventTypeName]Subsystem // + prototype?

	// subs maps each event type to a subsystem that's subscribed to it
	subs map[eventTypeName][]Subsystem
}

func NewEventBus() *EventBus {
	return &EventBus{
		pubs: map[eventTypeName]Subsystem{},
		subs: map[eventTypeName][]Subsystem{},
	}
}

func (bus *EventBus) DumpGraph() {
	// Construct the reverse of 'subs', e.g. map from subsystem to event types
	revSubs := map[Subsystem][]eventTypeName{}
	for typeName, ss := range bus.subs {
		for _, subsys := range ss {
			revSubs[subsys] = append(revSubs[subsys], typeName)
		}
	}

	for subsys, typs := range revSubs {
		fmt.Printf("%s:\n", subsys.SysName())
		for _, typ := range typs {
			fmt.Printf("\t%s\n", typ)
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
	// TODO locking
	// TODO rewind of changes on errors

	// Register event types published by this subsystem.
	for _, proto := range publishes {
		if subsys2, ok := bus.pubs[proto.typeName]; ok {
			return nil, fmt.Errorf("event type %q already registered by %s", proto.typeName, subsys2.SysName())
		}
		fmt.Printf("Registered event type %q\n", proto.typeName)
		bus.pubs[proto.typeName] = subsys
	}

	for _, proto := range subscribes {
		// Validate that some subsystem already publishes this event.
		if _, ok := bus.pubs[proto.typeName]; !ok {
			return nil, fmt.Errorf("event type %q is not known to the event bus", proto.typeName)
		}
		bus.subs[proto.typeName] = append(bus.subs[proto.typeName], subsys)
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
	typeName := getTypeName(ev)

	regSubsys, ok := h.bus.pubs[typeName]
	if !ok {
		return fmt.Errorf("event type %q not registered", typeName)
	}
	if regSubsys != h.subsys {
		return fmt.Errorf("event type %q registered to another subsystem: %s", typeName, regSubsys.SysName())
	}

	subs, ok := h.bus.subs[typeName]
	if !ok || len(subs) == 0 {
		return fmt.Errorf("no subscribers to %s", typeName)
	}

	// TODO locking
	for _, subsys := range subs {
		subsys.SysEventHandler(ev)
		// TODO: ^ short-circuit and return error to publishing subsystem?
		// ... or what do we want to do here?
	}

	return nil
}

// Unregister the subsystem from the registry.
func (h *EventBusHandle) Unregister() error {
	panic("TODO Unregister")
}

func getTypeName(x interface{}) eventTypeName {
	// TODO(JM): How expensive is the reflection here? Instead of constructing
	// the type name, we could just unpack the fat pointer to get a pointer
	// to type and use the pointer as index. Or just maintain a registry of events
	// somewhere and hand out integer identifiers? E.g.
	//   RegisterEvent(empty Event) EventId
	//   Publish(id EventId, ev event) ?
	typ := reflect.TypeOf(x).Elem()
	return eventTypeName(typ.PkgPath() + "." + typ.Name())
}
