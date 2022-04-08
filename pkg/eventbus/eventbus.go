// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package eventbus

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"
)

var (
	ErrEventBusNotFinal     = fmt.Errorf("cannot publish yet, EventBus still under construction")
	ErrEventBusAlreadyBuilt = fmt.Errorf("EventBus has already been built")
)

type Builder struct {
	sync.Mutex
	final atomicBool
	subs  []*subscriber
	bus   *EventBus
}

func NewBuilder() *Builder {
	return &Builder{
		subs: []*subscriber{},
		bus: &EventBus{
			pubs:  map[eventTypeId]*publisher{},
			types: map[eventTypeId]string{},
		},
	}
}

type UnsubscribeFunc func()

func (builder *Builder) Subscribe(empty Event, reason string, handler func(event Event) error) UnsubscribeFunc {
	builder.Lock()
	defer builder.Unlock()

	typeId := eventToTypeId(empty)

	from := "<unknown>"
	if pc, _, lineNum, ok := runtime.Caller(1); ok {
		if fn := runtime.FuncForPC(pc); fn != nil {
			from = fmt.Sprintf("%s#%d", fn.Name(), lineNum)
		}
	}

	sub := &subscriber{
		active:   atomicBool{1},
		reason:   reason,
		from:     from,
		typeId:   typeId,
		typeName: eventToTypeName(empty),
		handler:  handler,
	}
	builder.subs = append(builder.subs, sub)
	return func() { sub.active.unset() }
}

type PublishFunc func(ev Event) error

func (builder *Builder) Register(empty Event, name string) (PublishFunc, error) {
	builder.Lock()
	defer builder.Unlock()

	typeId := eventToTypeId(empty)
	typeName := eventToTypeName(empty)

	if _, ok := builder.bus.pubs[typeId]; ok {
		return nil, fmt.Errorf("publisher already registered for %s", typeName)
	}

	pub := &publisher{
		name:     name,
		typeId:   typeId,
		typeName: typeName,
		subs:     []*subscriber{},
	}
	builder.bus.pubs[typeId] = pub
	builder.bus.types[typeId] = typeName

	publish := func(ev Event) error {
		if !builder.final.get() {
			return ErrEventBusNotFinal
		}
		return pub.publish(ev)
	}
	return publish, nil
}

func (builder *Builder) Build() (*EventBus, error) {
	if !builder.final.casSet() {
		return nil, ErrEventBusAlreadyBuilt
	}

	bus := builder.bus

	// Wire up the subscribers to the publishers
	for _, sub := range builder.subs {
		pub, ok := bus.pubs[sub.typeId]
		if !ok {
			return nil, fmt.Errorf("cannot build eventbus: no publisher found for %s", sub.typeName)
		}
		pub.subs = append(pub.subs, sub)
	}
	return bus, nil
}

type Event interface {
	fmt.Stringer
}

type eventTypeId uint64

// eventToTypeId extracts event's type identifier
func eventToTypeId(ev Event) eventTypeId {
	type fatPtr struct {
		rtype uintptr
		w     uintptr
	}
	return eventTypeId((*fatPtr)(unsafe.Pointer(&ev)).rtype)
}

func eventToTypeName(x interface{}) string {
	return reflect.TypeOf(x).String()
}

// EventBus provides a publish-subscribe service for broadcasting events from a subsystem.
type EventBus struct {
	pubs  map[eventTypeId]*publisher
	types map[eventTypeId]string
}

func (bus *EventBus) PrintGraph() {
	fmt.Println("Event bus publishers and subscribers:")
	for _, pub := range bus.pubs {
		fmt.Printf("  %s [%s]:\n", pub.name, pub.typeName)
		for _, sub := range pub.subs {
			fmt.Printf("    %s [%s]\n", sub.reason, sub.from)
		}
	}
}

type publisher struct {
	typeId   eventTypeId
	typeName string
	name     string
	subs     []*subscriber
}

func (pub *publisher) publish(ev Event) error {
	typeId := eventToTypeId(ev)
	if typeId != pub.typeId {
		typeName := eventToTypeName(ev)
		return fmt.Errorf("event type mismatch, expected %s (%d), but got %s (%d)", pub.typeName, pub.typeId, typeName, typeId)
	}

	for i := range pub.subs {
		if pub.subs[i].active.get() {
			pub.subs[i].handler(ev)
		}
	}
	return nil
}

type subscriber struct {
	active   atomicBool
	reason   string
	from     string
	typeId   eventTypeId
	typeName string
	handler  func(ev Event) error
}

type atomicBool struct {
	v int32
}

func (b *atomicBool) get() bool {
	if atomic.LoadInt32(&b.v) == 0 {
		return false
	}
	return true
}

func (b *atomicBool) casSet() bool {
	return atomic.CompareAndSwapInt32(&b.v, 0, 1)
}

func (b *atomicBool) set() {
	atomic.StoreInt32(&b.v, 1)
}

func (b *atomicBool) unset() {
	atomic.StoreInt32(&b.v, 0)
}
