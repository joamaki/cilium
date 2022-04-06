package eventbus

import (
	"time"

	"github.com/cilium/cilium/pkg/logging"
	"github.com/cilium/cilium/pkg/logging/logfields"
)

//
// An example Cilium subsystem utilizing eventbus.
//

var (
	log = logging.DefaultLogger.WithField(logfields.LogSubsys, "example")
)

//
// Example subsystem events
//

// Ready event
type ExampleEventReady struct{}

func (e *ExampleEventReady) String() string { return "Ready" }

var ExampleEventReadyP = NewEventPrototype("Example subsystem is ready", &ExampleEventReady{})

// Foo event

type ExampleEventFoo struct {
	Timestamp time.Time
}

func (e *ExampleEventFoo) String() string { return "Foo has happened" }

var ExampleEventFooP = NewEventPrototype("The dreaded Foo event has happened", &ExampleEventFoo{})

//
// The example subsystem implementation
//

type ExampleSubsys struct {
	events *EventPublisher
}

func NewExampleSubsys(bus *EventBus) (*ExampleSubsys, error) {
	ex := &ExampleSubsys{}
	var err error
	ex.events, err = bus.RegisterSubsystem(
		ex,
		[]EventPrototype{ExampleEventReadyP, ExampleEventFooP},
		nil,
	)
	if err != nil {
		return nil, err
	}

	go ex.worker()

	return ex, nil
}

func (ex *ExampleSubsys) worker() {
	time.Sleep(time.Second)
	ex.events.Publish(&ExampleEventFoo{time.Now()})
}

func (ex *ExampleSubsys) SysName() string { return "example" }

func (ex *ExampleSubsys) SysEventHandler(ev Event) error {
	log.Infof("Received event: %s", ev)
	return nil
}
