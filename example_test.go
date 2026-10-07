package gsb_test

import (
	"fmt"

	"gsb"
)

func ExampleSolve() {
	p := &gsb.Problem{
		Materials: []gsb.Material{
			{Name: "Steel", Stock: 6, UnitCost: 3},
			{Name: "Alloy", Stock: 5, UnitCost: 4},
			{Name: "Weld", Stock: 2, UnitCost: 1}, // shared auxiliary
		},
		Demands: []gsb.Demand{
			{Name: "frame", Quantity: 2, Options: []gsb.Option{
				{Material: 0, Ratio: 2},
				{Material: 1, Ratio: 1, Aux: []gsb.AuxUse{{Material: 2, Amount: 1}}},
			}},
			{Name: "panel", Quantity: 1, Options: []gsb.Option{
				{Material: 0, Ratio: 1},
				{Material: 1, Ratio: 1, Aux: []gsb.AuxUse{{Material: 2, Amount: 1}}},
			}},
		},
	}
	s, err := gsb.Solve(p)
	if err != nil {
		panic(err)
	}
	fmt.Print(s.Report(p))
	// Output:
	// complete orders: 2
	// total cost: 28
	// consumption evidence:
	//   demand "frame": 2 unit(s) via option 0 [Steel x4]
	//   demand "frame": 2 unit(s) via option 1 [Alloy x2, Weld x2]
	//   demand "panel": 2 unit(s) via option 0 [Steel x2]
	// material usage (used/stock):
	//   Steel: 6/6
	//   Alloy: 2/5
	//   Weld: 2/2
}
