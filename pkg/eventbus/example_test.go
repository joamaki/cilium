package eventbus

import (
	"testing"
	"time"
)

func TestExampleSubsystem(t *testing.T) {
	builder := NewBuilder()

	_, err := NewExampleSubsys(builder)
	if err != nil {
		t.Fatal(err)
	}

	foo := false
	builder.Subscribe(
		new(ExampleEventFoo),
		"catch a foo",
		func(ev Event) error {
			foo = true
			return nil
		})

	bus, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}

	bus.PrintGraph()

	time.Sleep(2 * time.Second)

	if !foo {
		t.Fatal("no foo")
	}
}
