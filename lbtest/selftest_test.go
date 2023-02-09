package main

import (
	"context"
	"testing"
	"time"

	"github.com/cilium/cilium/lbtest/controlplane/servicemanager"
	"github.com/cilium/cilium/lbtest/datapath"
	"github.com/cilium/cilium/lbtest/datapath/monitor"

	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/option"

	"github.com/stretchr/testify/assert"
)

func TestDatapathLoadBalancing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	//logging.SetLogLevelToDebug()

	var st selftest
	h := hive.New(
		selftestCell,
		cell.Supply(monitor.MonitorConfig{EnableMonitor: true}),
		datapath.Cell,
		servicemanager.Cell,
		cell.Invoke(func(s selftest) { st = s }),
	)
	h.Viper().Set("self-test", false)
	h.Viper().Set("direct-routing-ip", "172.16.0.1")
	option.Config.SetDevices([]string{"dummy_lbst0"}) // TODO clean up Devices.

	assert.NoError(t, h.Start(ctx))
	assert.NoError(t, st.Run())
	assert.NoError(t, h.Stop(ctx))
}
