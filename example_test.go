package colarchive_test

import (
	"fmt"

	ca "prescreen.local/ps-f514817be4"
)

func ExampleArchive_Plan() {
	a := &ca.Archive{Groups: []ca.Group{{Blocks: []ca.Block{{
		Rows:   1000,
		Width:  3,
		Planes: []int{0, 1, 2},
		Nulls:  []ca.NullRun{{Rows: 1000, Null: false}},
	}}}}}
	plan, err := a.Plan(ca.Query{
		Rows: []ca.RowSpan{{Start: 0, Step: 1, Count: 1000}},
		Bits: []int{1},
	}, ca.Device{StartupCost: 10, ByteCost: 1, MaxBytes: 64})
	if err != nil {
		panic(err)
	}
	fmt.Println(plan.Cost, plan.TotalBytes, plan.ReadCount)
	for _, r := range plan.Reads {
		fmt.Println(r.Start, r.Length, r.Repeat, r.Stride)
	}
	// Output:
	// 145 125 2
	// 125 61 1 0
	// 186 64 1 0
}
