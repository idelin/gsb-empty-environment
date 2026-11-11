package netsec_test

import (
	"fmt"

	"example.org/netsec/q0461"
)

func ExampleAnalyze() {
	events := []netsec.Event{
		{Kind: netsec.KindWait, Actor: "A", Resources: []string{"rB"}},
		{Kind: netsec.KindWait, Actor: "B", Resources: []string{"rA"}},
		{Kind: netsec.KindSend, Actor: "A", Ref: "mA"},
		{Kind: netsec.KindReceive, Actor: "B", Ref: "mA", Produces: "rB"},
		{Kind: netsec.KindSend, Actor: "B", Ref: "mB"},
		{Kind: netsec.KindReceive, Actor: "A", Ref: "mB", Produces: "rA"},
	}
	report, err := netsec.Analyze(events, netsec.Options{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("subsets:", report.TotalSubsets)
	fmt.Println("anchors:", report.AnchorWaits)
	fmt.Println("ring closed:", report.RingClosed)
	for _, core := range report.Cores {
		fmt.Println("minimal core events:", core.Events)
		for _, chk := range core.RemovalChecks {
			fmt.Printf("remove #%d -> still stuck=%v\n", chk.RemovedIndex, chk.StillStuck)
		}
	}
	// Output:
	// subsets: 64
	// anchors: [0 1]
	// ring closed: true
	// minimal core events: [0 1 2 3 4 5]
	// remove #2 -> still stuck=false
	// remove #3 -> still stuck=false
	// remove #4 -> still stuck=false
	// remove #5 -> still stuck=false
}
