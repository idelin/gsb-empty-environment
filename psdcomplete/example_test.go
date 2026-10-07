package psdcomplete_test

import (
	"fmt"

	"psdcomplete"
)

// Complete a 3x3 symmetric matrix with unknowns chosen from finite
// candidate sets, minimizing the trace.
func Example() {
	spec, _ := psdcomplete.NewSpec(3)
	_ = spec.Fix(0, 0, psdcomplete.Rat(2))
	_ = spec.SetCandidates(0, 1, psdcomplete.Rat(-1), psdcomplete.Rat(0), psdcomplete.Rat(1))
	_ = spec.SetCandidates(0, 2, psdcomplete.Frac(1, 2), psdcomplete.Rat(1))
	_ = spec.SetCandidates(1, 1, psdcomplete.Rat(1), psdcomplete.Rat(2))
	_ = spec.SetCandidates(1, 2, psdcomplete.Rat(0), psdcomplete.Frac(-1, 3))
	_ = spec.SetCandidates(2, 2, psdcomplete.Rat(1), psdcomplete.Rat(3))

	res, err := psdcomplete.Solve(spec, nil)
	if err != nil {
		panic(err)
	}
	fmt.Print(res.String())

	// Cross-check with the independent exhaustive solver.
	brute, _ := psdcomplete.SolveBrute(spec)
	fmt.Println("brute-force agrees:", brute.OK == res.OK && brute.Trace.Cmp(res.Trace) == 0)

	// Output:
	// optimal assignment (minimal trace 4):
	//   [2  -1  1/2]
	//   [-1  1  -1/3]
	//   [1/2  -1/3  1]
	//
	// PSD certificate: 7 principal minors (all must be >= 0):
	//   det A[{1}] = 2 >= 0
	//   det A[{2}] = 1 >= 0
	//   det A[{3}] = 1 >= 0
	//   det A[{1,2}] = 1 >= 0
	//   det A[{1,3}] = 7/4 >= 0
	//   det A[{2,3}] = 8/9 >= 0
	//   det A[{1,2,3}] = 31/36 >= 0
	// independent check: exact LDL^T elimination pivots: [2, 1/2, 31/36] (all >= 0, PSD)
	// search stats: 5 nodes expanded, 1 leaves evaluated, 6 branches pruned
	//   prune: depth 5 [a12=-1 a13=1/2 a22=1 a23=-1/3 a33=3]: reachable trace >= 6 > best 4
	//   prune: depth 4 [a12=-1 a13=1/2 a22=1 a23=0]: trace can only tie best 4 and prefix is lexicographically larger
	//   prune: depth 3 [a12=-1 a13=1/2 a22=2]: reachable trace >= 5 > best 4
	//   prune: depth 2 [a12=-1 a13=1]: trace can only tie best 4 and prefix is lexicographically larger
	//   prune: depth 1 [a12=0]: trace can only tie best 4 and prefix is lexicographically larger
	//   prune: depth 1 [a12=1]: trace can only tie best 4 and prefix is lexicographically larger
	// brute-force agrees: true
}
