package main

import (
	"context"
	"time"

	"github.com/cilium/cilium/pkg/eventbus"
	"go.uber.org/fx"
)

func run(bus *eventbus.EventBus, quuxy *Quuxy, foob *Foob) {
	bus.DumpGraph()
	time.Sleep(5 * time.Second)
}

func main() {
	app := fx.New(
		fx.Provide(
			eventbus.NewEventBus,
			NewFoob,
			NewQuuxy,
		),
		fx.Invoke(run),
	)

	startCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		log.Fatal(err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := app.Stop(stopCtx); err != nil {
		log.Fatal(err)
	}
}
