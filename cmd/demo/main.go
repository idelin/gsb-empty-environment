// Command demo exercises the temporal package end to end: normalization
// edge cases, unbounded splitting, a spanning FOR PORTION update with
// adjacent merge, and a foreign-key coverage failure that rolls back.
package main

import (
	"errors"
	"fmt"

	temporal "prescreen.local/ps-db7888bb15"
)

func main() {
	fmt.Println("== normalization ==")
	fmt.Println("discrete (3,4) ->", temporal.MustParseIntRange("(3,4)"))
	fmt.Println("discrete [3,3] ->", temporal.NewDiscreteRange(3, 3, true, true))
	t := int64(1_700_000_000_000_000)
	fmt.Println("continuous (t,t] ->", temporal.NewTimeRange(t, t, false, true))
	fmt.Println("point [t,t] overlaps [t,t+1):",
		temporal.Point(t).Overlaps(temporal.NewTimeRange(t, t+1, true, false)))
	fmt.Println("point [t,t] overlaps (t,t+1):",
		temporal.Point(t).Overlaps(temporal.NewTimeRange(t, t+1, false, false)))

	fmt.Println("\n== unbounded difference ==")
	for _, p := range temporal.Difference(temporal.AllRange(temporal.Discrete), temporal.NewIntRange(5, 10)) {
		fmt.Println(" ", p)
	}

	fmt.Println("\n== FOR PORTION spanning rows + adjacent merge ==")
	tbl := temporal.NewTable("account_config", "tenant")
	tbl.CoalesceAdjacent = true
	must(tbl.Insert(temporal.Row{ID: "a", Key: temporal.Key{"tenant": 1}, Range: temporal.NewIntRange(0, 50), Payload: "v1"}))
	must(tbl.Insert(temporal.Row{ID: "b", Key: temporal.Key{"tenant": 1}, Range: temporal.NewIntRange(50, 100), Payload: "v1"}))
	must(tbl.UpdatePortion(temporal.Key{"tenant": 1}, temporal.NewIntRange(20, 80), "v2"))
	for _, r := range tbl.Rows {
		fmt.Printf("  %-6s %-8s payload=%v\n", r.ID, r.Range, r.Payload)
	}

	fmt.Println("\n== FK temporal coverage + rollback ==")
	s := temporal.NewSchema()
	parent := s.CreateTable("contract", "tenant", "cid")
	child := s.CreateTable("entry", "tenant", "cid")
	s.AddForeignKey(temporal.ForeignKeyCoverage{
		ChildTable: "entry", ChildColumns: []string{"tenant", "cid"},
		ParentTable: "contract", ParentColumns: []string{"tenant", "cid"},
	})
	must(parent.Insert(temporal.Row{Key: temporal.Key{"tenant": 1, "cid": 10}, Range: temporal.NewIntRange(0, 50)}))
	must(parent.Insert(temporal.Row{Key: temporal.Key{"tenant": 1, "cid": 10}, Range: temporal.NewIntRange(50, 100)}))
	must(child.Insert(temporal.Row{ID: "e", Key: temporal.Key{"tenant": 1, "cid": 10}, Range: temporal.NewIntRange(0, 100)}))

	before := len(child.Rows)
	err := child.UpdatePortion(
		temporal.Key{"tenant": 1, "cid": 10},
		temporal.NewIntRange(40, 60),
		temporal.Fields{"cid": 99},
	)
	fmt.Println("re-keyed update error:", err)
	fmt.Println("wraps ErrCoverage:", errors.Is(err, temporal.ErrCoverage))
	fmt.Printf("child rows after rollback: %d (before %d), valid=%v\n",
		len(child.Rows), before, s.Validate() == nil)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
