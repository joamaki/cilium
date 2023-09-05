package main

import (
	"fmt"

	"github.com/cilium/cilium/pkg/hive/cell"
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/index"
	"github.com/google/uuid"
	"golang.org/x/exp/slices"
)

// Define two tables, "baskets" and "apples".
var demoTables = cell.Group(
	statedb.NewTableCell[*Basket]("baskets", basketNameIndex, basketApplesIndex),
	statedb.NewTableCell[*Apple]("apples", appleIDIndex, appleColorIndex),
)

type BasketName = string

type Basket struct {
	Name   BasketName
	Apples []uuid.UUID
}

func (b *Basket) String() string {
	return fmt.Sprintf("Basket %q with apples %+v", b.Name, b.Apples)
}

func (b *Basket) Clone() *Basket {
	return &Basket{
		Name:   b.Name,
		Apples: slices.Clone(b.Apples),
	}
}

// basketNameIndex is a unique primary index for looking up a *Basket by its name.
var basketNameIndex = statedb.Index[*Basket, BasketName]{
	Name: "name",
	// FromObject translates an object into one or more keys.
	// The primary index must return only one key and be unique.
	FromObject: func(obj *Basket) index.KeySet {
		return index.NewKeySet(index.String(obj.Name))
	},
	FromKey: index.String,
	Unique:  true,
}

// basketApplesIndex is a non-unique multi-index for looking up a basket containing
// a specific apple.
var basketApplesIndex = statedb.Index[*Basket, uuid.UUID]{
	Name: "apples",
	// Here we define a non-unique multi-index that returns multiple keys.
	// Non-unique indexes are implemented by appending the primary key of the
	// object to the key.
	FromObject: func(obj *Basket) index.KeySet {
		ks := index.NewKeySet()
		for _, uuid := range obj.Apples {
			ks.Append(uuidKey(uuid))
		}
		return ks
	},
	FromKey: uuidKey,
	Unique:  false,
}

type Apple struct {
	ID    uuid.UUID
	Color string
}

func uuidKey(uuid uuid.UUID) index.Key {
	key, _ := uuid.MarshalBinary()
	return key
}

// appleIDIndex is the unique primary index for looking up an *Apple by its ID.
var appleIDIndex = statedb.Index[*Apple, uuid.UUID]{
	Name: "id",
	FromObject: func(obj *Apple) index.KeySet {
		return index.NewKeySet(uuidKey(obj.ID))
	},
	FromKey: uuidKey,
	Unique:  true,
}

// appleColorIndex is a non-unique single-index for looking up apples with a
// certain color.
var appleColorIndex = statedb.Index[*Apple, string]{
	Name: "color",
	FromObject: func(obj *Apple) index.KeySet {
		return index.NewKeySet(index.String(obj.Color))
	},
	FromKey: index.String,
	Unique:  false,
}

func newApple(color string) *Apple {
	return &Apple{
		ID:    uuid.New(),
		Color: color,
	}
}
