package state

import (
	"fmt"

	memdb "github.com/hashicorp/go-memdb"
)

// TODO better/more generic name for this
type Reflector interface {
	ProcessChanges(memdb.Changes) error
}

type exampleReflector struct {
}

func (r *exampleReflector) ProcessChanges(changes memdb.Changes) error {
	fmt.Printf("exampleReflector: %+v\n", changes)

	// TODO: Store the
	return nil
}
