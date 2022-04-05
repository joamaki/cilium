package eventbus

import (
	"fmt"
	"time"
)

type eventTypeName string

type Event interface {
	// TODO what should be included here?
	//
	fmt.Stringer
}

// EventPrototype describes an event. Prototypes are used when registering
// a subsystem to define what events it subscribes to or may publish.
type EventPrototype struct {
	description string
	empty       Event
	typeName    eventTypeName
}

// NewEventProto constructs an event prototype used for describing
// events that a subsystem can publish or subscribe to.
func NewEventPrototype(description string, empty Event) EventPrototype {
	return EventPrototype{description, empty, getTypeName(empty)}

}

// annEvent is an event annotated by the event bus.
type annEvent struct {
	event     Event
	source    string
	emittedAt time.Time
	// ... ?
}
