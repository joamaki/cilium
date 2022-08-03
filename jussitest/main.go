package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"time"

	"go.uber.org/fx"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/cilium/cilium/daemon/cmd"
	"github.com/cilium/cilium/pkg/k8s"
	cilium_v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	slim_corev1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/api/core/v1"
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	kubeCfgPath := path.Join(homeDir, ".kube", "config")

	k8s.Configure("", kubeCfgPath, 10.0, 5)
	t0 := time.Now()
	var app = fx.New(
		fx.WithLogger(cmd.NewPrettyAppLogger),
		k8s.ClientModule,
		k8s.ResourcesModule,
		fx.Invoke(testStart),
	)
	fmt.Printf("🚀 Graph completed in %s. Starting...\n", time.Now().Sub(t0))
	app.Run()
}

type TestIn struct {
	fx.In

	Client          *k8s.Clientset
	Nodes           k8s.NodesObservable
	CiliumEndpoints k8s.CiliumEndpointsObservable
	Info            k8s.NodeInformationObservable
}

func testStart(lc fx.Lifecycle, in TestIn) {
	ctx, cancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			runTest(ctx, in)
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			return nil
		},
	})
}

func runTest(ctx context.Context, in TestIn) {
	go in.Nodes.Observe(ctx,
		func(event k8s.NodesEvent) error {
			event.Dispatch(
				func() { fmt.Printf(">>> Nodes have synced\n") },
				func(key k8s.Key, node *slim_corev1.Node) {
					fmt.Printf(">>> Node %s updated: %s\n", key, node)
				},
				func(key k8s.Key) {
					fmt.Printf(">>> Node %s deleted\n", key)
				},
			)
			return nil
		})

	go in.CiliumEndpoints.Observe(ctx,
		func(event k8s.CiliumEndpointsEvent) error {
			event.Dispatch(
				func() { fmt.Printf(">>> CiliumEndpoints have synced\n") },
				func(key k8s.Key, ce *cilium_v2.CiliumEndpoint) {
					fmt.Printf(">>> CiliumEndpoint %s updated: %#v\n", key, ce)
				},
				func(key k8s.Key) {
					fmt.Printf(">>> CiliumEndpoint %s deleted\n", key)
				},
			)
			return nil
		})

	go in.Info.Observe(ctx,
		func(info k8s.NodeInformation) error {
			fmt.Printf("Node info: %#v\n", info.Node)
			return nil
		})

	nodes, err := in.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	fmt.Printf("Nodes: %#v (error: %s)\n", nodes, err)
}
