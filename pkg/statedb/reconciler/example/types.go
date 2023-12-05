package main

import (
	"github.com/cilium/cilium/pkg/statedb"
	"github.com/cilium/cilium/pkg/statedb/index"
	"github.com/cilium/cilium/pkg/statedb/reconciler"
	"github.com/spf13/pflag"
)

type Memo struct {
	Name    string
	Content string
	Status  reconciler.Status
}

func (memo *Memo) GetStatus() reconciler.Status {
	return memo.Status
}

func (memo *Memo) WithStatus(new reconciler.Status) *Memo {
	memo2 := *memo
	memo2.Status = new
	return &memo2
}

var MemoNameIndex = statedb.Index[*Memo, string]{
	Name: "name",
	FromObject: func(memo *Memo) index.KeySet {
		return index.NewKeySet(index.String(memo.Name))
	},
	FromKey: index.String,
	Unique:  true,
}

var MemoStatusIndex = reconciler.NewStatusIndex[*Memo]((*Memo).GetStatus)

func NewMemoTable() (statedb.RWTable[*Memo], error) {
	return statedb.NewTable[*Memo](
		"memos",
		MemoNameIndex,
		MemoStatusIndex,
	)
}

type Config struct {
	Directory string
}

func (def Config) Flags(flags *pflag.FlagSet) {
	flags.String("directory", "memos", "Target directory")
}
