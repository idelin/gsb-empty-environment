package disclosure_test

import (
	"fmt"

	"prescreen.local/ddd-2056-20261122-v2/disclosure"
)

func ExampleReview() {
	table := disclosure.Table{Cells: [][]disclosure.Cell{
		{{Actual: 4, Min: 3, Max: 5}, {Actual: 6, Min: 5, Max: 7}},
		{{Actual: 6, Min: 5, Max: 7}, {Actual: 4, Min: 3, Max: 5}},
	}}

	req := &disclosure.Request{
		Table: table,
		Constraints: []disclosure.Constraint{
			{
				ID:      "row0",
				GroupID: "margins",
				Terms: []disclosure.Term{
					{Row: 0, Col: 0, Coeff: 1},
					{Row: 0, Col: 1, Coeff: 1},
				},
				RHS: 10,
			},
			{
				ID:      "extra",
				GroupID: "extra",
				Terms: []disclosure.Term{
					{Row: 0, Col: 0, Coeff: 1},
					{Row: 0, Col: 1, Coeff: 1},
					{Row: 1, Col: 0, Coeff: 1},
				},
				RHS: 16,
			},
		},
		Groups: []disclosure.SuppressionGroup{
			{ID: "extra", Cost: 5},
			{ID: "margins", Cost: 10},
		},
		Sensitive: []disclosure.SensitiveCell{{Row: 0, Col: 0, K: 2}},
		Budget:    100,
	}

	decision, err := disclosure.Review(req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("approved:", decision.Approved)
	fmt.Println("hidden:", decision.HiddenGroupIDs)
	fmt.Println("cost:", decision.TotalCost)
	report := decision.Reports[0]
	fmt.Println("feasible:", report.FeasibleValues)
	fmt.Println("witnesses:", len(report.Witnesses))
	// Output:
	// approved: true
	// hidden: []
	// cost: 0
	// feasible: [3 4 5]
	// witnesses: 3
}
