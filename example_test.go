package recipex_test

import (
	"fmt"

	"recipex"
)

func ExampleSolve() {
	demands := []recipex.Demand{
		{ID: "order-1", Quantity: 2, Options: []recipex.Option{
			{Material: "steel", Ratio: 3, Aux: "flux", AuxRatio: 1, Cost: 5},
			{Material: "alum", Ratio: 4, Cost: 7},
		}},
		{ID: "order-2", Quantity: 1, Options: []recipex.Option{
			{Material: "steel", Ratio: 2, Cost: 4},
		}},
	}
	inventory := map[string]int64{"steel": 20, "alum": 20, "flux": 20}

	res, err := recipex.Solve(demands, inventory)
	if err != nil {
		panic(err)
	}
	fmt.Printf("orders=%d cost=%d\n", res.Orders, res.Cost)
	for _, a := range res.Assignments {
		fmt.Printf("%s: %s x%d (aux %s x%d)\n",
			a.DemandID, a.Option.Material, a.MaterialUse, a.Option.Aux, a.AuxUse)
	}
	for _, e := range res.Evidence {
		fmt.Printf("material=%s used=%d remaining=%d\n", e.Material, e.Used, e.Remaining)
	}
	// Output:
	// orders=2 cost=14
	// order-1: steel x6 (aux flux x6)
	// order-2: steel x2 (aux  x0)
	// material=alum used=0 remaining=20
	// material=flux used=6 remaining=14
	// material=steel used=8 remaining=12
}
