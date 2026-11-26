package bgpsim_test

import (
	"testing"

	bgpsim "prescreen.local/ps-b15ac20600"
)

// TestIGPTieBrokenByLowestRouterID covers the final tie-break: all metric
// steps through IGP are equal (same MED too), so the route carrying the
// lowest RouterID wins regardless of input slice order.
func TestIGPTieBrokenByLowestRouterID(t *testing.T) {
	build := func(reverse bool) *bgpsim.Config {
		routes := []bgpsim.Route{
			{Prefix: "192.0.2.128/25", Egress: "e1", RouterID: "4.4.4.4", NeighborAS: 65010, MED: 50, LocalPref: 100, ASPathLen: 2, Origin: bgpsim.OriginIGP},
			{Prefix: "192.0.2.128/25", Egress: "e2", RouterID: "2.2.2.2", NeighborAS: 65010, MED: 50, LocalPref: 100, ASPathLen: 2, Origin: bgpsim.OriginIGP},
		}
		if reverse {
			routes[0], routes[1] = routes[1], routes[0]
		}
		return &bgpsim.Config{
			Nodes: []bgpsim.Node{
				{ID: "rr", RouterID: "10.2.0.1", Reflector: true},
				{ID: "e1", RouterID: "10.2.1.1", Border: true},
				{ID: "e2", RouterID: "10.2.1.2", Border: true},
			},
			Clusters: []bgpsim.Cluster{
				{ID: "9", Reflectors: []bgpsim.NodeID{"rr"}, Clients: []bgpsim.NodeID{"e1", "e2"}},
			},
			ExternalRoutes: routes,
			// Equal IGP cost to both egresses.
			IGP: map[bgpsim.NodeID]map[bgpsim.NodeID]int{
				"rr": {"e1": 5, "e2": 5},
				"e1": {"e2": 5},
				"e2": {"e1": 5},
			},
		}
	}

	for _, reverse := range []bool{false, true} {
		res, err := bgpsim.Simulate(build(reverse))
		if err != nil {
			t.Fatalf("Simulate(reverse=%v): %v", reverse, err)
		}
		if !res.Converged {
			t.Fatalf("reverse=%v status=%s, want converged", reverse, res.Status)
		}
		got := res.BestRoute("rr", "192.0.2.128/25")
		if got == nil {
			t.Fatalf("reverse=%v: rr has no best route", reverse)
		}
		if got.Egress != "e2" || got.RouterID != "2.2.2.2" {
			t.Fatalf("reverse=%v best = egress %s rid %s, want e2 / 2.2.2.2 (lowest RouterID)",
				reverse, got.Egress, got.RouterID)
		}

		choice := res.Best["rr"]["192.0.2.128/25"]
		var loser *bgpsim.CandidateEvaluation
		for i := range choice.Candidates {
			if !choice.Candidates[i].Selected {
				loser = &choice.Candidates[i]
			}
		}
		if loser == nil {
			t.Fatalf("reverse=%v: expected a recorded losing candidate", reverse)
		}
		if loser.EliminatedAt != bgpsim.StageRouterID {
			t.Fatalf("reverse=%v elimination stage = %s, want RouterID", reverse, loser.EliminatedAt)
		}
	}
}

// TestCompareRouterIDOrdering checks numeric (not lexical) ordering of IDs.
func TestCompareRouterIDOrdering(t *testing.T) {
	cases := []struct {
		a, b bgpsim.RouterID
		want int
	}{
		{"2.2.2.2", "10.0.0.1", -1}, // numeric: 0x02... < 0x0a...
		{"1.1.1.1", "1.1.1.1", 0},
		{"255.255.255.255", "0.0.0.0", 1},
	}
	for _, tc := range cases {
		if got := bgpsim.CompareRouterID(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareRouterID(%s,%s)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
