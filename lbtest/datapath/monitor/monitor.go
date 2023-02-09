package monitor

import (
	"context"
	"fmt"

	"github.com/cilium/cilium/lbtest/datapath/loader"
	"github.com/cilium/cilium/pkg/datapath/link"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/monitor/agent"
	"github.com/cilium/cilium/pkg/monitor/agent/consumer"
	"github.com/cilium/cilium/pkg/monitor/format"
)

type MonitorConfig struct {
	EnableMonitor bool
}

var Cell = cell.Module(
	"monitor-agent",
	"Monitor displays events from BPF",

	cell.Invoke(registerMonitorAgent),
)

type monitorConsumer struct {
	printer *format.MonitorFormatter
}

func newMonitorConsumer() consumer.MonitorConsumer {
	linkCache := link.NewLinkCache()
	printer := format.NewMonitorFormatter(format.DEBUG, linkCache)
	return &monitorConsumer{printer}
}

// NotifyAgentEvent implements consumer.MonitorConsumer
func (*monitorConsumer) NotifyAgentEvent(typ int, message interface{}) {
	fmt.Printf("||| AgentEvent: typ=%d, message=%v\n", typ, message)
}

// NotifyPerfEvent implements consumer.MonitorConsumer
func (c *monitorConsumer) NotifyPerfEvent(data []byte, cpu int) {
	//fmt.Printf("||| PerfEvent: len(data)=%d, cpu=%d, type=%d\n", len(data), cpu, messageType)
	c.printer.FormatSample(data, cpu)
}

// NotifyPerfEventLost implements consumer.MonitorConsumer
func (*monitorConsumer) NotifyPerfEventLost(numLostEvents uint64, cpu int) {
	fmt.Printf("||| PerfEventLost: numLostEvents=%d, cpu=%d", numLostEvents, cpu)
}

var _ consumer.MonitorConsumer = &monitorConsumer{}

func registerMonitorAgent(lc hive.Lifecycle, r cell.StatusReporter, cfg MonitorConfig, _ *loader.Loader /* so that events map is pinned first */) {
	if !cfg.EnableMonitor {
		r.Down("Monitor disabled")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(hive.Hook{
		OnStart: func(hive.HookContext) error {
			defer r.OK("Monitoring")
			c := newMonitorConsumer()
			a := agent.NewAgent(ctx)
			a.RegisterNewConsumer(c)
			return a.AttachToEventsMap(8)
		},
		OnStop: func(hive.HookContext) error {
			cancel()
			return nil
		},
	})
}
