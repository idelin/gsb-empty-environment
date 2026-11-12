package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0469"
)

func ExampleAnalyze_duplicateConsumption() {
	events := []netsec.Event{
		{Kind: netsec.KindBegin, Subject: "alice", SessionNonce: "n1", Operation: "login"},
		{Kind: netsec.KindEnd, Subject: "alice", SessionNonce: "n1", Operation: "login"},
		{Kind: netsec.KindEnd, Subject: "alice", SessionNonce: "n1", Operation: "login"},
	}

	report, err := netsec.Analyze(events)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("feasible:", report.Feasible)
	fmt.Printf("hall gap: %d ends vs %d begin(s)\n",
		len(report.Witness.Ends), len(report.Witness.NeighborBegins))

	check, _ := netsec.CrossCheck(events, 0)
	fmt.Println("cross-check consistent:", check.Consistent)

	// Output:
	// feasible: false
	// hall gap: 2 ends vs 1 begin(s)
	// cross-check consistent: true
}

func ExampleEnumerateMatchings() {
	events := []netsec.Event{
		{Kind: netsec.KindBegin, Subject: "alice", SessionNonce: "n1", Operation: "login"},
		{Kind: netsec.KindBegin, Subject: "alice", SessionNonce: "n1", Operation: "login"},
		{Kind: netsec.KindEnd, Subject: "alice", SessionNonce: "n1", Operation: "login"},
		{Kind: netsec.KindEnd, Subject: "alice", SessionNonce: "n1", Operation: "login"},
	}

	enumeration, _ := netsec.EnumerateMatchings(events, 0)
	fmt.Println("complete matchings:", len(enumeration.Matchings))

	// Output:
	// complete matchings: 2
}
