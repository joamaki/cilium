package cmd

import (
	"context"
	"fmt"

	gopsAgent "github.com/google/gops/agent"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"go.uber.org/fx"

	"github.com/cilium/cilium/pkg/option"
)

var gopsModule = fx.Module(
	"gops",
	fx.Invoke(registerGops),
)

type gops struct {
	addr string
	log  *logrus.Entry
}

func registerGops(lc fx.Lifecycle) {
	addr := fmt.Sprintf("127.0.0.1:%d", viper.GetInt(option.GopsPort))
	addrField := logrus.Fields{"address": addr}
	g := gops{addr, log.WithFields(addrField)}
	lc.Append(fx.Hook{OnStart: g.start, OnStop: g.stop})
}

func (g gops) start(context.Context) error {
	g.log.Info("Started gops server")
	return gopsAgent.Listen(gopsAgent.Options{
		Addr:                   g.addr,
		ReuseSocketAddrAndPort: true,
	})
}

func (g gops) stop(context.Context) error {
	gopsAgent.Close()
	g.log.Info("Stopped gops server")
	return nil
}
