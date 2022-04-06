package eventbus

import (
	"testing"
	"time"
)

func TestExampleSubsystem(t *testing.T) {
	bus := NewEventBus()

	_, err := NewExampleSubsys(bus)
	if err != nil {
		t.Fatal(err)
	}

	tsys := &TestSubsys{"Test", 0}
	bus.RegisterSubsystem(tsys, nil, []EventPrototype{ExampleEventFooP})

	time.Sleep(2 * time.Second)
}
