package binom_test

import (
	"fmt"

	"binom"
)

func Example() {
	exact := binom.Exact(50, 25)
	fmt.Println("C(50, 25) =", exact)

	calc := binom.NewCalculator(1 << 20)
	results, err := calc.ModBatch([]binom.Query{
		{N: 50, K: 25, M: 1 << 20}, // 2^20: high prime power
		{N: 50, K: 25, M: 12},      // composite, factorials not invertible
		{N: 50, K: 25, M: 1_000_000_007},
		{N: 50, K: 25, M: 1},       // everything is 0 mod 1
		{N: 50, K: 60, M: 1 << 20}, // k out of range: C = 0
	})
	if err != nil {
		panic(err)
	}
	for _, r := range results {
		fmt.Println(r)
	}
	// Output:
	// C(50, 25) = 126410606437752
	// 714104
	// 0
	// 605552882
	// 0
	// 0
}
