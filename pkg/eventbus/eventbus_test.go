package eventbus

import (
	"fmt"
	"testing"
)

type TestEventA struct{}

func (e *TestEventA) String() string {
	return "TestEventA"
}

var TestEventA_Proto = NewEventPrototype(
	"This is TestEventA",
	&TestEventA{},
)

type TestEventB struct{}

func (e *TestEventB) String() string {
	return "TestEventB"
}

var TestEventB_Proto = NewEventPrototype(
	"This is TestEventB",
	&TestEventB{},
)

type TestEventC struct{}

func (e *TestEventC) String() string {
	return "TestEventC"
}

var TestEventC_Proto = NewEventPrototype(
	"This is TestEventC",
	&TestEventC{},
)

type TestSubsys struct {
	name string
}

func (s *TestSubsys) SysName() string { return s.name }

func (s *TestSubsys) SysEventHandler(ev Event) error {
	fmt.Printf("[%s]: Received: %s\n", s.name, ev)
	return nil
}

var subsysA = &TestSubsys{"SubsysA"}
var subsysB = &TestSubsys{"SubsysB"}
var subsysC = &TestSubsys{"SubsysC"}

func TestEventBus(t *testing.T) {

	bus := NewEventBus()

	// A publishes TestEventA
	handleA, err := bus.RegisterSubsystem(
		subsysA,
		[]EventPrototype{TestEventA_Proto},
		[]EventPrototype{})
	if err != nil {
		t.Fatal(err)
	}

	// B publishes TestEventB, and subscribes to TestEventA
	handleB, err := bus.RegisterSubsystem(
		subsysB,
		[]EventPrototype{TestEventB_Proto},
		[]EventPrototype{TestEventA_Proto})
	if err != nil {
		t.Fatal(err)
	}

	// Check that one cannot subscribe to unknown event types
	_, err = bus.RegisterSubsystem(
		subsysC,
		[]EventPrototype{},
		[]EventPrototype{TestEventC_Proto})
	if err == nil {
		t.Fatal("Expected registering of subsystem with subscription to unknown event to fail")
	}

	// C publishes TestEventC, and subscribes to TestEventA and TestEventB
	handleC, err := bus.RegisterSubsystem(
		subsysC,
		[]EventPrototype{TestEventC_Proto},
		[]EventPrototype{TestEventA_Proto, TestEventB_Proto})
	if err != nil {
		t.Fatal(err)
	}

	bus.DumpGraph()

	fmt.Printf("Publishing TestEventA:\n")
	// A can publish TestEventA
	err = handleA.Publish(&TestEventA{})
	if err != nil {
		t.Fatal(err)
	}

	fmt.Printf("Publishing TestEventB:\n")
	// B can publish TestEventB
	err = handleB.Publish(&TestEventB{})
	if err != nil {
		t.Fatal(err)
	}

	// C cannot publish TestEventA
	err = handleC.Publish(&TestEventA{})
	if err == nil {
		t.Fatal("handleC.Publish(TestEventA) succeeded")
	}

}
