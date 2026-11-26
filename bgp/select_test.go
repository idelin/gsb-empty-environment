package bgp

import (
	"math/rand"
	"testing"
)

func rid(t *testing.T, s string) uint32 {
	t.Helper()
	id, err := ParseRouterID(s)
	if err != nil {
		t.Fatalf("ParseRouterID(%q): %v", s, err)
	}
	return id
}

// permutations returns every ordering of the supplied slice.
func permutations(paths []*Path) [][]*Path {
	var out [][]*Path
	var walk func(k int)
	walk = func(k int) {
		if k == len(paths) {
			cp := make([]*Path, len(paths))
			copy(cp, paths)
			out = append(out, cp)
			return
		}
		for i := k; i < len(paths); i++ {
			paths[k], paths[i] = paths[i], paths[k]
			walk(k + 1)
			paths[k], paths[i] = paths[i], paths[k]
		}
	}
	walk(0)
	return out
}

// oscillationPaths builds the classic non-transitive three-path MED topology:
// two paths from AS 65001 whose MED ordering conflicts with the IGP cost
// ordering of the AS 65002 path.
func oscillationPaths(t *testing.T) []*Path {
	return []*Path{
		{
			ID: "p1", LocalPref: 100, LearnedFrom: PeerEBGP,
			ASPath: []Segment{{Type: SegASSequence, ASes: []uint32{65001, 65100}}},
			Origin: OriginIGP, MEDPresent: true, MED: 50, IGPCost: 5,
			NextHopReachable: true, NextHop: "10.0.0.1", PeerIP: "192.0.2.1",
			PeerRouterID: rid(t, "1.1.1.1"),
		},
		{
			ID: "p2", LocalPref: 100, LearnedFrom: PeerEBGP,
			ASPath: []Segment{{Type: SegASSequence, ASes: []uint32{65001, 65100}}},
			Origin: OriginIGP, MEDPresent: true, MED: 100, IGPCost: 1,
			NextHopReachable: true, NextHop: "10.0.0.2", PeerIP: "192.0.2.2",
			PeerRouterID: rid(t, "2.2.2.2"),
		},
		{
			ID: "p3", LocalPref: 100, LearnedFrom: PeerEBGP,
			ASPath: []Segment{{Type: SegASSequence, ASes: []uint32{65002, 65200}}},
			Origin: OriginIGP, MEDPresent: true, MED: 200, IGPCost: 3,
			NextHopReachable: true, NextHop: "10.0.0.3", PeerIP: "192.0.2.3",
			PeerRouterID: rid(t, "3.3.3.3"),
		},
	}
}

// TestDeterministicMEDNoOscillation is the headline acceptance case: all six
// arrival orders (plus random shuffles) must select exactly the same path.
func TestDeterministicMEDNoOscillation(t *testing.T) {
	sel := NewSelector(Config{
		LocalAS: 65000, RouterID: rid(t, "10.0.0.254"),
		DeterministicMED: true, MissingMED: MissingMEDAsZero,
	})
	base := oscillationPaths(t)

	var want string
	for i, perm := range permutations(append([]*Path(nil), base...)) {
		res := sel.Select(perm)
		if res.Best == nil {
			t.Fatalf("permutation %d: no best path", i)
		}
		if i == 0 {
			want = res.Best.ID
		} else if res.Best.ID != want {
			t.Fatalf("permutation %d: got %s, want %s (arrival-order dependence)",
				i, res.Best.ID, want)
		}
	}

	// Group 65001 internally prefers the lower MED (p1). Cross-group MED is
	// ignored, so p3 then beats p1 on IGP cost (3 < 5).
	if want != "p3" {
		t.Fatalf("deterministic winner = %s, want p3", want)
	}

	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		shuffled := append([]*Path(nil), base...)
		rng.Shuffle(len(shuffled), func(a, b int) {
			shuffled[a], shuffled[b] = shuffled[b], shuffled[a]
		})
		if got := sel.Select(shuffled).Best.ID; got != want {
			t.Fatalf("shuffle %d: got %s, want %s", i, got, want)
		}
	}
}

// TestLegacyMEDArrivalOrderDependence proves the legacy pairwise fold can
// oscillate while deterministic MED does not.
func TestLegacyMEDArrivalOrderDependence(t *testing.T) {
	sel := NewSelector(Config{
		LocalAS: 65000, RouterID: rid(t, "10.0.0.254"),
		DeterministicMED: false, MissingMED: MissingMEDAsZero,
	})
	base := oscillationPaths(t)

	winners := map[string]bool{}
	for _, perm := range permutations(append([]*Path(nil), base...)) {
		winners[sel.Select(perm).Best.ID] = true
	}
	if len(winners) < 2 {
		t.Fatalf("legacy mode expected order-dependent winners, got %v", winners)
	}
}

// TestAlwaysCompareMED forces cross-group MED so the global lowest MED wins
// regardless of arrival order.
func TestAlwaysCompareMED(t *testing.T) {
	sel := NewSelector(Config{
		LocalAS: 65000, RouterID: rid(t, "10.0.0.254"),
		DeterministicMED: true, AlwaysCompareMED: true,
	})
	base := oscillationPaths(t)

	for i, perm := range permutations(append([]*Path(nil), base...)) {
		if got := sel.Select(perm).Best.ID; got != "p1" {
			t.Fatalf("permutation %d: got %s, want p1 (lowest global MED)", i, got)
		}
	}
}

// TestMissingMEDPolicies checks both missing-MED conventions.
func TestMissingMEDPolicies(t *testing.T) {
	mk := func(id, peerIP string, medPresent bool, med uint32) *Path {
		return &Path{
			ID: id, LocalPref: 100,
			ASPath: []Segment{{Type: SegASSequence, ASes: []uint32{65001}}},
			Origin: OriginIGP, MEDPresent: medPresent, MED: med,
			LearnedFrom: PeerEBGP, NextHopReachable: true,
			PeerIP: peerIP, PeerRouterID: rid(t, "9.9.9.9"),
		}
	}
	paths := func() []*Path {
		return []*Path{
			mk("missing", "192.0.2.1", false, 0),
			mk("present", "192.0.2.2", true, 100),
		}
	}

	t.Run("missing treated as zero", func(t *testing.T) {
		sel := NewSelector(Config{LocalAS: 65000, DeterministicMED: true})
		if got := sel.Select(paths()).Best.ID; got != "missing" {
			t.Fatalf("missing-as-zero: best = %s, want missing", got)
		}
	})
	t.Run("missing treated as infinity", func(t *testing.T) {
		sel := NewSelector(Config{
			LocalAS: 65000, DeterministicMED: true,
			MissingMED: MissingMEDAsInfinity,
		})
		if got := sel.Select(paths()).Best.ID; got != "present" {
			t.Fatalf("missing-as-worst: best = %s, want present", got)
		}
	})
}

// TestTieBreakOrdering walks the ordered tie breaks with focused pairs.
func TestTieBreakOrdering(t *testing.T) {
	base := func(id string) *Path {
		return &Path{
			ID: id, LocalPref: 100,
			ASPath: []Segment{{Type: SegASSequence, ASes: []uint32{65001}}},
			Origin: OriginIGP, MEDPresent: true, MED: 10,
			LearnedFrom: PeerIBGP, IGPCost: 100,
			NextHopReachable: true, PeerIP: "192.0.2.10",
			PeerRouterID: rid(t, "10.10.10.10"),
		}
	}
	sel := NewSelector(Config{
		LocalAS: 65000, RouterID: rid(t, "10.0.0.254"),
		ClusterID: rid(t, "10.0.0.200"), DeterministicMED: true,
	})
	cases := []struct {
		name string
		mut  func(p *Path)
		want string
	}{
		{"local pref", func(p *Path) { p.LocalPref = 200 }, "mut"},
		{"shorter as path", func(p *Path) {
			p.ASPath = []Segment{{Type: SegASSequence, ASes: []uint32{65001, 65002}}}
		}, "base"},
		{"origin", func(p *Path) { p.Origin = OriginIncomplete }, "base"},
		{"path type ebgp", func(p *Path) { p.LearnedFrom = PeerEBGP }, "mut"},
		{"igp cost", func(p *Path) { p.IGPCost = 50 }, "mut"},
		{"cluster list length", func(p *Path) {
			p.ClusterList = []uint32{1, 2}
		}, "base"},
		{"router id", func(p *Path) {
			p.OriginatorID = rid(t, "1.1.1.1")
		}, "mut"},
		{"peer ip", func(p *Path) { p.PeerIP = "192.0.2.1" }, "mut"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b := base("base"), base("mut")
			tc.mut(b)
			got := sel.Select([]*Path{a, b}).Best.ID
			if got != tc.want {
				t.Fatalf("%s: best = %s, want %s", tc.name, got, tc.want)
			}
			if got := sel.Select([]*Path{b, a}).Best.ID; got != tc.want {
				t.Fatalf("%s reversed: best = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestGroupWinnersExposed verifies per-neighbour-AS winners are reported.
func TestGroupWinnersExposed(t *testing.T) {
	sel := NewSelector(Config{LocalAS: 65000, DeterministicMED: true})
	res := sel.Select(oscillationPaths(t))
	if len(res.GroupWinners) != 2 {
		t.Fatalf("groups = %d, want 2", len(res.GroupWinners))
	}
	if res.GroupWinners[65001].ID != "p1" {
		t.Fatalf("65001 winner = %s, want p1 (lower MED)", res.GroupWinners[65001].ID)
	}
	if res.GroupWinners[65002].ID != "p3" {
		t.Fatalf("65002 winner = %s, want p3", res.GroupWinners[65002].ID)
	}
}
