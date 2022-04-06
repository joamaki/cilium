package eventbus

import (
	"fmt"
	"reflect"
	"unsafe"
)

type eventTypeName string

type Event interface {
	// TODO what should be included here?
	fmt.Stringer
}

// EventPrototype captures event's type. Prototypes are used when registering
// a subsystem to define what events it subscribes to or may publish.
type EventPrototype struct {
	description string
	empty       Event
	typeName    eventTypeName
	typeId      eventTypeId
}

func (p EventPrototype) String() string {
	return fmt.Sprintf("%s: %s", p.typeName, p.description)
}

// NewEventProto constructs an event prototype used for describing
// events that a subsystem can publish or subscribe to.
func NewEventPrototype(description string, empty Event) EventPrototype {
	return EventPrototype{description, empty, eventToTypeName(empty), eventToTypeId(empty)}
}

/*
// annEvent is an event annotated by the event bus.
type annEvent struct {
	event     Event
	source    string
	emittedAt time.Time
	// ... ?
}*/

type eventTypeId uint64

// eventToTypeId extracts event's type identifier
func eventToTypeId(ev Event) eventTypeId {
	type fatPtr struct {
		rtype uintptr
		w     uintptr
	}
	return eventTypeId((*fatPtr)(unsafe.Pointer(&ev)).rtype)
}

func eventToTypeName(x interface{}) eventTypeName {
	typ := reflect.TypeOf(x).Elem()
	return eventTypeName(typ.PkgPath() + "." + typ.Name())
}
