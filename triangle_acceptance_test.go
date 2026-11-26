package bgpsim_test

import (
	"testing"

	bgpsim "prescreen.local/ps-b15ac20600"
)

func triangleConfig(acme bool) *bgpsim.Config {
	ids := []bgpsim.NodeID{"rr1", "rr2", "rr3", "b1", "b2", "b3"}
	nodes := []bgpsim.Node{
		{ID: "rr1", RouterID: "10.0.0.1", Reflector: true},
		{ID: "rr2", RouterID: "10.0.0.2", Reflector: true},
		{ID: "rr3", RouterID: "10.0.0.3", Reflector: true},
		{ID: "b1", RouterID: "10.0.1.1", Border: true},
		{ID: "b2", RouterID: "10.0.1.2", Border: true},
		{ID: "b3", RouterID: "10.0.1.3", Border: true},
	}
	clusters := []bgpsim.Cluster{
		{ID: "1", Reflectors: []bgpsim.NodeID{"rr1"}, Clients: []bgpsim.NodeID{"b1"}},
		{ID: "2", Reflectors: []bgpsim.NodeID{"rr2"}, Clients: []bgpsim.NodeID{"b2"}},
		{ID: "3", Reflectors: []bgpsim.NodeID{"rr3"}, Clients: []bgpsim.NodeID{"b3"}},
	}
	routes := []bgpsim.Route{
		{Prefix: "198.51.100.0/24", Egress: "b1", RouterID: "1.1.1.1", NeighborAS: 10, MED: 10, LocalPref: 100, ASPathLen: 2, Origin: bgpsim.OriginIGP},
		{Prefix: "198.51.100.0/24", Egress: "b2", RouterID: "2.2.2.2", NeighborAS: 20, MED: 20, LocalPref: 100, ASPathLen: 2, Origin: bgpsim.OriginIGP},
		{Prefix: "198.51.100.0/24", Egress: "b3", RouterID: "3.3.3.3", NeighborAS: 10, MED: 30, LocalPref: 100, ASPathLen: 2, Origin: bgpsim.OriginIGP},
	}
	// Columns are rr1 rr2 rr3 b1 b2 b3. Only costs towards b1..b3 drive
	// selection; RR rows encode the cyclic IGP attraction.
	matrix := [][]int{
		{0, 9, 9, 2, 1, 3}, // rr1 -> b1=2 b2=1 b3=3
		{9, 0, 9, 3, 2, 1}, // rr2 -> b1=3 b2=2 b3=1
		{9, 9, 0, 1, 3, 2}, // rr3 -> b1=1 b2=3 b3=2
		{2, 3, 1, 0, 9, 9},
		{1, 2, 3, 9, 0, 9},
		{3, 1, 2, 9, 9, 0},
	}
	igp, err := bgpsim.DenseIGP(ids, matrix)
	if err != nil {
		panic(err)
	}
	return &bgpsim.Config{
		Nodes:          nodes,
		Clusters:       clusters,
		ExternalRoutes: routes,
		IGP:            igp,
		Options:        bgpsim.Options{AlwaysCompareMED: acme},
	}
}

func rrVector(res *bgpsim.Result, round int) []string {
	out := []string{}
	for _, rr := range []bgpsim.NodeID{"rr1", "rr2", "rr3"} {
		c := res.Rounds[round].Choices[rr]["198.51.100.0/24"]
		if c == nil || c.Best == nil {
			out = append(out, "-")
		} else {
			out = append(out, string(c.Best.Egress))
		}
	}
	return out
}

const trianglePrefix = "198.51.100.0/24"

// TestThreeReflectorMEDTriangleOscillates is the headline acceptance case:
// the classic three-reflector triangle with MED comparable only inside the
// same adjacent AS produces a stable period-2 oscillation. The round vectors
// are expanded and compared exactly.
func TestThreeReflectorMEDTriangleOscillates(t *testing.T) {
	res, err := bgpsim.Simulate(triangleConfig(false))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}

	if res.Status != bgpsim.StatusOscillating || res.Converged {
		t.Fatalf("status=%s converged=%v, want oscillating", res.Status, res.Converged)
	}
	if res.Oscillation == nil {
		t.Fatal("Oscillation detail is nil")
	}
	osc := res.Oscillation
	if osc.FirstRound != 1 || osc.RepeatRound != 3 || osc.Period != 2 {
		t.Fatalf("oscillation = first %d repeat %d period %d, want 1/3/2",
			osc.FirstRound, osc.RepeatRound, osc.Period)
	}
	if len(osc.Cycle) != 2 {
		t.Fatalf("cycle length = %d, want 2", len(osc.Cycle))
	}
	if res.Rounds[1].Fingerprint != res.Rounds[3].Fingerprint {
		t.Fatal("round 3 must repeat the round-1 state")
	}

	wantVectors := [][]string{
		{"-", "-", "-"},
		{"b1", "b2", "b3"},
		{"b2", "b3", "b1"},
		{"b1", "b2", "b3"},
	}
	if len(res.Rounds) != len(wantVectors) {
		t.Fatalf("executed %d rounds, want %d", len(res.Rounds), len(wantVectors))
	}
	for i, want := range wantVectors {
		if got := rrVector(res, i); !equalStrings(got, want) {
			t.Fatalf("round %d RR best egress = %v, want %v", i, got, want)
		}
	}

	// Incomparability markers: b1 carries MED 10 (lower than b2's MED 20)
	// but AS 10 != AS 20, so MED is skipped and b1 is eliminated on IGP.
	var foundCasualty bool
	for _, m := range res.MEDIncomparableEliminations {
		if m.Round == 2 && m.Node == "rr1" &&
			m.Candidate.Egress == "b1" && m.Winner.Egress == "b2" &&
			m.Stage == bgpsim.StageIGPCost && m.CandidateLowerMED {
			foundCasualty = true
		}
	}
	if !foundCasualty {
		t.Fatalf("missing lower-MED incomparability casualty rr1/b1->b2: %+v",
			res.MEDIncomparableEliminations)
	}

	// Final-best-relative marker: at rr1 in round 2, b1 loses to the final
	// best b2 through IGP after cross-AS MED was skipped.
	rr1Round2 := res.Rounds[2].Choices["rr1"][trianglePrefix]
	var marked bool
	for _, cand := range rr1Round2.Candidates {
		if cand.Route.Egress == "b1" && cand.IncomparabilityEliminated {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("rr1 round2 b1 not marked IncomparabilityEliminated: %+v", rr1Round2.Candidates)
	}

	// The same-neighbor-AS pair (b1,b3, both AS 10) must genuinely compare
	// MED somewhere: b3 (MED 30) loses to b1 (MED 10) on StageMED.
	var foundMEDComparison bool
	for _, rnd := range res.Rounds {
		for _, choices := range rnd.Choices {
			for _, ch := range choices {
				if ch == nil {
					continue
				}
				for _, cand := range ch.Candidates {
					if cand.EliminatedAt == bgpsim.StageMED && cand.MEDCompared {
						foundMEDComparison = true
					}
				}
			}
		}
	}
	if !foundMEDComparison {
		t.Fatal("expected a same-AS MED elimination, none recorded")
	}
}

// TestAlwaysCompareMEDStopsOscillation reruns the exact triangle with
// always-compare-med: MED 10 route b1 becomes the global winner and the
// system reaches a unique fixed point.
func TestAlwaysCompareMEDStopsOscillation(t *testing.T) {
	res, err := bgpsim.Simulate(triangleConfig(true))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if !res.Converged || res.Status != bgpsim.StatusConverged {
		t.Fatalf("status=%s converged=%v, want converged", res.Status, res.Converged)
	}
	if res.Oscillation != nil {
		t.Fatalf("unexpected oscillation: %+v", res.Oscillation)
	}

	wantVectors := [][]string{
		{"-", "-", "-"},
		{"b1", "b2", "b3"},
		{"b1", "b1", "b1"},
		{"b1", "b1", "b1"},
	}
	if len(res.Rounds) != len(wantVectors) {
		t.Fatalf("executed %d rounds, want %d", len(res.Rounds), len(wantVectors))
	}
	for i, want := range wantVectors {
		if got := rrVector(res, i); !equalStrings(got, want) {
			t.Fatalf("round %d RR best egress = %v, want %v", i, got, want)
		}
	}
	for _, rr := range []bgpsim.NodeID{"rr1", "rr2", "rr3"} {
		if got := res.BestRoute(rr, trianglePrefix); got == nil || got.Egress != "b1" {
			t.Fatalf("%s best = %v, want b1", rr, got)
		}
	}
	if len(res.MEDIncomparableEliminations) != 0 {
		t.Fatalf("always-compare-med must leave no incomparability marks, got %d",
			len(res.MEDIncomparableEliminations))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
