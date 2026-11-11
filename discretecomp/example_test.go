package discretecomp_test

import (
	"fmt"
	"math/big"

	"example.org/netsec/q0432/discretecomp"
)

func ExampleCompose() {
	// One binary mechanism under two neighbouring data sets.
	p := discretecomp.Distribution{
		"0": big.NewRat(3, 4),
		"1": big.NewRat(1, 4),
	}
	q := discretecomp.Distribution{
		"0": big.NewRat(1, 2),
		"1": big.NewRat(1, 2),
	}
	m, err := discretecomp.NewMechanism(p, q)
	if err != nil {
		panic(err)
	}

	// The service publishes this mechanism twice, independently.
	profile, err := discretecomp.Compose([]*discretecomp.Mechanism{m, m})
	if err != nil {
		panic(err)
	}

	// Exact slack for the multiplicative budget e^epsilon = 2 (P||Q direction).
	delta, err := profile.Delta(big.NewRat(2, 1), discretecomp.DeltaPOverQ)
	if err != nil {
		panic(err)
	}
	fmt.Println("exact delta:", delta.RatString())

	// Budgeted enumeration: leave at most 10% of the finite mass un-inspected.
	res, err := discretecomp.ComposeTruncated(
		[]*discretecomp.Mechanism{m, m},
		big.NewRat(2, 1),
		discretecomp.DeltaPOverQ,
		discretecomp.TruncateOptions{MassBudget: big.NewRat(1, 10)},
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("bounds:", res.Bounds.Lower.RatString(), res.Bounds.Upper.RatString())

	// Output:
	// exact delta: 1/16
	// bounds: 1/16 1/8
}
