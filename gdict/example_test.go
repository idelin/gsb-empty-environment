package gdict_test

import (
	"fmt"

	"prescreen.local/ps-bf896dd1e3/gdict"
)

func ExampleSolve() {
	a, b := gdict.Text("a"), gdict.Text("b")
	pre, _ := gdict.NewPreorder("en", [][]gdict.Key{{a}, {b}})
	cfg := gdict.Config{
		Partitions: []gdict.Partition{{
			Name: "p1", Version: "en",
			Entries: []gdict.Entry{{OldCode: 0, Key: a}, {OldCode: 1, Key: b}},
			Cuts:    []gdict.Cut{{Name: "live", OldCode: 0, Bound: gdict.LE}},
		}},
		Preorders: []gdict.Preorder{pre},
		Space:     gdict.CodeSpace{Limit: 100},
	}
	plan, err := gdict.Solve(cfg)
	if err != nil {
		panic(err)
	}
	if err := gdict.Verify(cfg, plan); err != nil {
		panic(err)
	}
	fmt.Printf("pages=%d exceptions=%d used=%d\n", plan.Stats.Pages, plan.Stats.ExceptionPages, plan.Stats.UsedCodes)
	// Output:
	// pages=1 exceptions=0 used=2
}
