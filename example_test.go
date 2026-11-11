package wafdecision_test

import (
	"fmt"

	"wafdecision"
)

// ExampleWAFRule models a WAF rule combining a cheap header regex, an
// expensive body parser and a signature scan:
//
//	(header_match OR (body_parser AND signature_scan))
//
// The compiler orders the detectors to minimize the worst-case total cost.
func Example_wAFRule() {
	rule := wafdecision.OrOf(
		wafdecision.NewLeaf(0),
		wafdecision.AndOf(wafdecision.NewLeaf(1), wafdecision.NewLeaf(2)),
	)
	specs := []wafdecision.LeafSpec{
		{ID: 0, Cost: 2, SelLo: 0.30, SelHi: 0.40},  // header regex
		{ID: 1, Cost: 25, SelLo: 0.05, SelHi: 0.15}, // body parser
		{ID: 2, Cost: 8, SelLo: 0.60, SelHi: 0.80},  // signature scan
	}
	g, err := wafdecision.Compile(rule, specs)
	if err != nil {
		panic(err)
	}
	report, err := g.Verify()
	if err != nil {
		panic(err)
	}

	outcome := g.Decide(map[int]wafdecision.Detector{
		0: func() bool { return false },
		1: func() bool { return true },
		2: func() bool { return true },
	})

	fmt.Printf("verdict=%d cost=%d calls=%d checked=%d worst=%d\n",
		outcome.Verdict, outcome.Stats.TotalCost, outcome.Stats.CallCount,
		report.Assignments, g.WorstCost)
	// Output: verdict=1 cost=35 calls=3 checked=8 worst=35
}
