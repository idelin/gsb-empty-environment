package netsec

import "fmt"

// ExampleCheck compares the two overflow policies on the strict-order small
// model. It shows how a verification host invokes the package and reads the
// verdict, statistics and violating trace.
func ExampleCheck() {
	spec := StrictOrderHandshake(1)
	budget := Budget{MaxStates: 500, MaxSteps: 100}

	for _, policy := range []OverflowPolicy{DropNew, DropOld} {
		rep := Check(spec, policy, budget)
		fmt.Printf("%-8s verdict=%-6s drops=%d visited=%d\n",
			policy, rep.Verdict, rep.Stats.DroppedSends, rep.Stats.VisitedStates)
		if rep.Verdict == Unsafe {
			ev, _ := lastSuccess(rep.Trace)
			fmt.Printf("         %s entered %s missing seqs %v tags %v\n",
				ev.Endpoint, ev.State, ev.Missing, ev.MissingTags)
		}
	}

	// Output:
	// drop-new verdict=safe   drops=2 visited=11
	// drop-old verdict=unsafe drops=1 visited=5
	//          R entered R_DONE missing seqs [1] tags []
}
