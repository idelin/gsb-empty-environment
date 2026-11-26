package expohist_test

import (
	"fmt"

	"prescreen.local/ps-2c403e57ff/expohist"
)

func ExampleHistogram() {
	// Fine-grained collector: scale 4 means each power of two spans 16 buckets.
	fine, _ := expohist.New(4, 0)
	fine.Observe(0.012)
	fine.Observe(0.35)
	fine.Observe(-1.75)

	// Coarse collector at scale 0; Merge unifies both at the smaller scale.
	coarse, _ := expohist.New(0, 0)
	if err := coarse.Merge(fine); err != nil {
		panic(err)
	}

	positive := coarse.Positive()
	negative := coarse.Negative()
	fmt.Println("scale:", coarse.Scale())
	fmt.Printf("positive buckets: offset=%d counts=%v\n", positive.Offset, positive.Counts)
	fmt.Printf("negative buckets: offset=%d counts=%v\n", negative.Offset, negative.Counts)
	fmt.Println("count/zero:", coarse.Count(), coarse.ZeroCount())

	bounds := coarse.PositiveBounds()
	if len(bounds) > 0 {
		fmt.Printf("first positive bucket: [%.4f, %.4f)\n", bounds[0].Lower, bounds[0].Upper)
	}
	// Output:
	// scale: 0
	// positive buckets: offset=-7 counts=[1 0 0 0 0 1]
	// negative buckets: offset=0 counts=[1]
	// count/zero: 3 0
	// first positive bucket: [0.0078, 0.0156)
}
