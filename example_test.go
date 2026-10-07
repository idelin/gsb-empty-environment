package uniparse_test

import (
	"fmt"

	"uniparse"
)

func Example() {
	f, err := uniparse.ParseText(`
start S
S[num=N]  -> NP[num=N] VP[num=N]
NP[num=N] -> Det N[num=N]
Det       -> "the"
N[num=sg] -> "dog"
VP[num=N] -> V[num=N]
V[num=sg] -> "barks"
`, []string{"the", "dog", "barks"})
	if err != nil {
		panic(err)
	}
	for _, t := range f.Trees(4) {
		fmt.Print(t)
	}
	// Output:
	// S{num=sg}
	//   NP{num=sg}
	//     Det
	//       "the"
	//     N{num=sg}
	//       "dog"
	//   VP{num=sg}
	//     V{num=sg}
	//       "barks"
}
