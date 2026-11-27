package txn_test

import (
	"fmt"

	"prescreen.local/ps-19c50a7725/txn"
)

// ExampleSolve demonstrates requirement/ordering orthogonality: Requires pulls
// the database into the transaction, while the explicit After edge alone
// decides the startup sequence.
func ExampleSolve() {
	g := &txn.Graph{Units: map[string]*txn.Unit{
		"app.service": {
			Name:     "app.service",
			Requires: []string{"db.service"},
			After:    []string{"db.service"},
		},
		"db.service": {Name: "db.service"},
	}}

	plan, err := txn.Solve(g, txn.Snapshot{}, txn.Request{
		Unit: "app.service",
		Job:  txn.JobStart,
		Mode: txn.ModeFail,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for i, layer := range plan.Layers {
		fmt.Printf("layer %d: %v\n", i, layer)
	}
	// Output:
	// layer 0: [db.service]
	// layer 1: [app.service]
}
