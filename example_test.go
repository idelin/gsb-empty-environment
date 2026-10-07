package fparse_test

import (
	"fmt"

	"fparse"
)

func Example() {
	g, err := fparse.ParseGrammar(`
S[num=N] -> NP[num=N] VP[num=N]
NP[num=sg] -> "he"
NP[num=pl] -> "they"
VP[num=sg] -> "runs"
VP[num=pl] -> "run"
`)
	if err != nil {
		panic(err)
	}
	forest, err := g.ParseForest([]string{"they", "run"}, "S")
	if err != nil {
		panic(err)
	}
	fmt.Println("analyses:", forest.NumAnalyses(100))
	for _, t := range forest.Trees(fparse.DefaultMaxTrees) {
		fmt.Print(t)
	}
	if err := forest.Verify(0); err != nil {
		panic(err)
	}
	// Output:
	// analyses: 1
	// S[num=pl]
	//   NP[num=pl]
	//     "they"
	//   VP[num=pl]
	//     "run"
}
