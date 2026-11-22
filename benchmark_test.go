package destplan_test

import (
	"testing"

	dp "prescreen.local/ddd-9511-20261122-v2"
)

func BenchmarkSolveTwentyEdges(b *testing.B) {
	keys := []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}, {ID: "d", Role: dp.KeyData}}
	edges := make([]dp.WrapEdge, 0, 20)
	for i := 0; i < 20; i++ {
		edges = append(edges, dp.WrapEdge{ID: benchName(i), From: "R", To: "d", Cost: 1})
	}
	p := &dp.Problem{
		Snapshots: []dp.Snapshot{{ID: "s", Root: leaf(1, 0, "d")}},
		Graph:     dp.KeyGraph{Keys: keys, Edges: edges},
		Destroy:   []dp.Requirement{{Snapshot: "s", Interval: dp.Interval{Lo: 0, Hi: 1}}},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := dp.Solve(p); err != nil {
			b.Fatal(err)
		}
	}
}

func benchName(i int) string {
	return "bench-edge-" + string(rune('a'+i))
}
