package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0069"
)

func ExampleFindShortestWitness() {
	p := netsec.NewProgram(
		[]netsec.Event{
			netsec.Enter(), netsec.LoadRoot(), netsec.Exit(),
			netsec.Enter(), netsec.Dereference(),
		},
		[]netsec.Event{
			netsec.ReplaceRoot(1), netsec.RetireObject(1),
			netsec.TryAdvance(), netsec.TryAdvance(),
		},
	)
	w, err := netsec.FindShortestWitness(p)
	if err != nil {
		panic(err)
	}
	fmt.Println(w.Length())
	fmt.Println(w.Actors())
	fmt.Println(w.Violation.Ref)
	// Output:
	// 9
	// [0 1 0 0 1 1 1 0 0]
	// 1@g0
}

func ExampleReviewProposed() {
	steps := []netsec.ProposedStep{
		{Thread: 0, Event: netsec.Enter()},
		{Thread: 1, Event: netsec.ReplaceRoot(1)},
		{Thread: 0, Event: netsec.LoadRoot()},
		{Thread: 0, Event: netsec.Quiescent()},
	}
	result, err := netsec.ReviewProposed(steps)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.FirstUnsafe.Step)
	fmt.Println(result.FirstUnsafe.Kind)
	// Output:
	// 3
	// event
}
