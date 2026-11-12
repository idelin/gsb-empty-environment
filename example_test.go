package netsec_test

import (
	"fmt"

	"example.org/netsec/q0297"
)

func ExampleCompare() {
	oldGraph := netsec.Graph{
		Root: "app",
		Nodes: []netsec.Node{
			{ID: "app"},
			{ID: "tool"},
		},
		Edges: []netsec.Edge{
			{From: "app", To: "tool", When: netsec.Flag("linux")},
		},
	}
	newGraph := netsec.Graph{
		Root: "app",
		Nodes: []netsec.Node{
			{ID: "app"},
			{ID: "tool", Capabilities: []string{"postinstall"}},
		},
		Edges: oldGraph.Edges,
	}

	report, err := netsec.Compare(oldGraph, newGraph)
	if err != nil {
		panic(err)
	}
	fmt.Println(report.Capabilities())
	for _, addition := range report.Added {
		fmt.Println(addition.Capability, addition.PathString(), addition.Config)
	}
	// Output:
	// [postinstall]
	// postinstall app -> tool {linux=true}
}
