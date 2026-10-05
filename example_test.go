package fuzzdict_test

import (
	"fmt"

	"fuzzdict"
)

func Example() {
	dict := fuzzdict.New([]string{"abc", "abd", "bca", "cab"})
	res, _ := dict.Search(fuzzdict.Query{
		Pattern:       "abc",
		K:             3,
		Costs:         fuzzdict.Costs{Insert: 1, Delete: 1, Replace: 1, Transpose: 1},
		MaxCost:       -1,
		MaxExpansions: -1,
	})
	for _, r := range res {
		fmt.Println(r.Word, r.Cost, r.Proven)
	}
	// Output:
	// abc 0 true
	// abd 1 true
	// bca 2 true
}
