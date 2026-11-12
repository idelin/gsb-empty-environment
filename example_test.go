package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0472"
)

func ExampleCheck() {
	// Replacement returns at time 5; the stale grant on the retired
	// challenge invokes at time 6 and wrongly reports success.
	h := netsec.History{Operations: []netsec.Operation{
		{ID: 1, Kind: netsec.Create, ChallengeID: "c1", Answer: "a", Invoke: 0, Return: 1, Result: netsec.OK},
		{ID: 2, Kind: netsec.Replace, ChallengeID: "c1", ReplacementID: "c2", Answer: "b", Invoke: 2, Return: 5, Result: netsec.OK},
		{ID: 3, Kind: netsec.Grant, ChallengeID: "c1", Invoke: 6, Return: 7, Result: netsec.OK},
	}}

	report, err := netsec.Check(h)
	if err != nil {
		panic(err)
	}
	fmt.Println(report.Linearizable)
	fmt.Println(report.Counterexample.Reason != "")
	// Output:
	// false
	// true
}

func ExampleEnumerateShortHistories() {
	_, stats, err := netsec.EnumerateShortHistories(netsec.EnumerateConfig{
		MaxOps:      5,
		WrongLimit:  2,
		IncludeBugs: true,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(stats.Histories > 0)
	fmt.Println(stats.BugsFound > 0)
	// Output:
	// true
	// true
}
