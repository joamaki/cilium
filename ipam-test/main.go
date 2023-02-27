package main

import (
	"github.com/spf13/cobra"

	"github.com/cilium/cilium/pkg/hive"
)

var (
	testHive = hive.New(
		App,
	)
)

func main() {
	cmd := &cobra.Command{
		Use: "test",
		Run: func(_ *cobra.Command, flags []string) {
			testHive.Run()
		},
	}
	cmd.AddCommand(testHive.Command())
	testHive.RegisterFlags(cmd.Flags())
	cmd.Execute()
}
