package main

import (
	"fmt"
	"os"

	"github.com/hashicorp/go-memdb"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/cilium/cilium/memdb/controllers"
	"github.com/cilium/cilium/memdb/datapath"
	"github.com/cilium/cilium/memdb/datasources"
	"github.com/cilium/cilium/memdb/state"
	"github.com/cilium/cilium/memdb/tables"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/k8s/client"
)

var Hive = hive.New(
	client.Cell,
	serverCell,

	state.Cell,
	controllers.Cell,
	datasources.Cell,

	tables.Cell,

	datapath.Cell,

	//cell.Invoke(debugState),
)

func main() {
	cmd := &cobra.Command{
		Use: "memdb",
		Run: func(*cobra.Command, []string) {
			if err := Hive.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", err)
				os.Exit(1)
			}
		},
	}
	cmd.AddCommand(Hive.Command())
	Hive.RegisterFlags(cmd.Flags())
	cmd.Execute()
}

type debugReflector struct {
	log logrus.FieldLogger
}

func (d *debugReflector) ProcessChanges(changes memdb.Changes) error {
	d.log.Infof("Changes: %+v\n", changes)
	return nil
}

func debugState(log logrus.FieldLogger, s *state.State) {
	s.SetReflector(&debugReflector{log})

}

/*
// test the injected tables
func testDummyTable(s *state.State, dummyTable state.Table[*datasources.Dummy]) {
	go func() {
		for {
			txR := s.Read()
			dummiesR := dummyTable.Read(txR)
			it, err := dummiesR.Get(state.All)
			if err != nil {
				panic(err)
			}
			state.ProcessEach(it,
				func(d *datasources.Dummy) error {
					fmt.Printf("dummy: %+v\n", d)
					return nil
				})

			// Wait for dummy table to change.
			<-it.Invalidated()
		}
	}()

}*/
