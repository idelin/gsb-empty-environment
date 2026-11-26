package sdf_test

import (
	"fmt"

	"prescreen.local/ps-0819d8d48a/sdf"
)

func ExampleSolve() {
	p := &sdf.Problem{
		Actors: 2,
		Edges: []sdf.Edge{
			{Src: -1, Dst: 0, Cons: 1},
			{Src: 0, Dst: 1, Prod: 1, Cons: 1},
			{Src: 1, Dst: -1, Prod: 1},
		},
		Cost:    []int64{1, 1},
		Res:     sdf.Resources{Descriptors: 2},
		WindowN: 4,
		WindowM: 1,
		MaxCap:  4,
		Inputs: []sdf.Input{
			{EdgeID: 0, Period: 4, Count: 1, FirstTime: 0, Gap: 4, Runs: 1},
		},
	}
	r, err := sdf.Solve(p)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(r.Status, r.Cap, r.TotalCap)
	if err := r.Schedule.Verify(p, 2); err != nil {
		fmt.Println("replay error:", err)
		return
	}
	if err := sdf.VerifyCertificate(p, r.Cap, r.Cert); err != nil {
		fmt.Println("certificate error:", err)
		return
	}
	fmt.Println("verified")
	// Output:
	// feasible [2 1 1] 4
	// verified
}
