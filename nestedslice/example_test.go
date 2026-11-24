package nestedslice_test

import (
	"fmt"

	"prescreen.local/ps-763423cf12/nestedslice"
)

func ExampleBuildPlan() {
	values := &nestedslice.Buffer{ID: "v", Kind: nestedslice.ValuesBuffer, Length: 4, ValueWidth: 1}
	validity := &nestedslice.Buffer{ID: "valid", Kind: nestedslice.ValidityBuffer, Length: 4}
	root := &nestedslice.Node{
		ID:         "root",
		Name:       "root",
		Kind:       nestedslice.PrimitiveNode,
		Length:     4,
		ValueWidth: 1,
		Value:      values,
		Validity:   validity,
		NullRows:   nestedslice.EmptySet(0, 4),
	}
	rows := nestedslice.SetOf(nestedslice.AP{First: 0, Step: 2, Count: 2})
	plan, err := nestedslice.BuildPlan(root, nestedslice.Query{TopRows: rows}, nestedslice.Options{
		StartupCost: 5,
		PerByteCost: 1,
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("segments=%d cost=%d rootRows=%d\n",
		len(plan.Segments), plan.Cost, plan.Root.Rows.Count())
	// Output: segments=2 cost=14 rootRows=2
}
