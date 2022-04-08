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

// Foo event

type ExampleEventFoo struct {
	Timestamp time.Time
}

func (e *ExampleEventFoo) String() string { return "Foo has happened" }

//
// The example subsystem implementation
//

type ExampleSubsys struct {
	publishReady PublishFunc
	publishFoo   PublishFunc
}

func NewExampleSubsys(builder *Builder) (*ExampleSubsys, error) {
	ex := &ExampleSubsys{}
	var err error

	ex.publishReady, err = builder.Register(new(ExampleEventReady), "Example is ready")
	if err != nil {
		return nil, err
	}

	ex.publishFoo, err = builder.Register(new(ExampleEventFoo), "Foo")
	if err != nil {
		return nil, err
	}

	go ex.worker()

	return ex, nil
}

func (ex *ExampleSubsys) worker() {
	time.Sleep(time.Second)
	ex.publishFoo(&ExampleEventFoo{time.Now()})
}
