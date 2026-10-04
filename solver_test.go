package main

import (
	"math/rand"
	"testing"
)

func TestMeetRound(t *testing.T) {
	cases := []struct{ a, b, want int }{
		{0, 1, 1}, {2, 3, 1}, {0, 3, 2}, {4, 6, 2},
		{0, 7, 3}, {3, 4, 3}, {0, 15, 4}, {7, 8, 4},
	}
	for _, c := range cases {
		if got := meetRound(c.a, c.b); got != c.want {
			t.Errorf("meetRound(%d,%d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func mustProblem(t *testing.T, cfg *Config) *Problem {
	t.Helper()
	p, err := NewProblem(cfg)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	return p
}

func TestSolveBasic(t *testing.T) {
	// 4 players, two teams of two. Best: split each team across halves so
	// both pairs can only meet in the final (round 2, penalty 1 each).
	cfg := &Config{
		Size: 4,
		Players: []Player{
			{ID: 1, Team: "A"}, {ID: 2, Team: "A"},
			{ID: 3, Team: "B"}, {ID: 4, Team: "B"},
		},
	}
	p := mustProblem(t, cfg)
	sol := p.Solve()
	if sol.cost != 2 {
		t.Fatalf("cost = %d, want 2", sol.cost)
	}
	// Tie-break: lexicographically smallest ID sequence among optima.
	want := []int{0, 2, 1, 3} // player indices for IDs 1,3,2,4
	for s := range want {
		if sol.assign[s] != want[s] {
			t.Fatalf("assign = %v, want %v", sol.assign, want)
		}
	}
	if got := p.Cost(sol); got != sol.cost {
		t.Fatalf("recomputed cost %d != %d", got, sol.cost)
	}
}

func TestLockedAndBye(t *testing.T) {
	// 3 players in a 4-slot bracket, slot 0 locked to player 1.
	cfg := &Config{
		Size: 4,
		Players: []Player{
			{ID: 1, Team: "A"}, {ID: 2, Team: "A"}, {ID: 3, Team: "B"},
		},
		Locked: map[int]int{0: 1},
	}
	p := mustProblem(t, cfg)
	sol := p.Solve()
	if sol.assign[0] != 0 {
		t.Fatalf("slot 0 should hold player 1")
	}
	// Player 2 (team A) should land in slot 2 or 3 (other half, meet in
	// final, penalty 1) rather than slot 1 (round 1, penalty 2).
	if sol.cost != 1 {
		t.Fatalf("cost = %d, want 1", sol.cost)
	}
}

func TestTierConstraint(t *testing.T) {
	cfg := &Config{
		Size: 4,
		Players: []Player{
			{ID: 1, Team: "A", Tier: 1}, {ID: 2, Team: "A", Tier: 2},
			{ID: 3, Team: "B", Tier: 1}, {ID: 4, Team: "B", Tier: 2},
		},
		SlotTiers: []int{1, 1, 2, 2},
	}
	p := mustProblem(t, cfg)
	sol := p.Solve()
	// Tier 1 players occupy slots 0,1 -> team A's #1 meets #2? No: #2 is
	// tier 2. Each team has one player per half, so both pairs meet in the
	// final.
	if sol.cost != 2 {
		t.Fatalf("cost = %d, want 2", sol.cost)
	}
	for s, pi := range sol.assign {
		if p.players[pi].Tier != cfg.SlotTiers[s] {
			t.Fatalf("slot %d: player tier %d != slot tier %d",
				s, p.players[pi].Tier, cfg.SlotTiers[s])
		}
	}
}

func TestValidation(t *testing.T) {
	bad := []*Config{
		{Size: 3, Players: []Player{{ID: 1, Team: "A"}}},                            // not power of two
		{Size: 32, Players: []Player{{ID: 1, Team: "A"}}},                           // too big
		{Size: 2, Players: []Player{{ID: 1, Team: "A"}, {ID: 1, Team: "B"}}},        // dup ID
		{Size: 2, Players: []Player{{ID: 1, Team: "A"}}, Locked: map[int]int{0: 9}}, // unknown player
		{Size: 2, Players: []Player{{ID: 1, Team: "A"}}, Locked: map[int]int{5: 1}}, // slot range
		{ // tier count mismatch
			Size:      2,
			Players:   []Player{{ID: 1, Team: "A", Tier: 1}, {ID: 2, Team: "B", Tier: 1}},
			SlotTiers: []int{1, 2},
		},
	}
	for i, cfg := range bad {
		if _, err := NewProblem(cfg); err == nil {
			t.Errorf("case %d: expected error, got nil", i)
		}
	}
}

// TestSolveMatchesBruteForce randomizes small instances and checks the
// branch-and-bound solver against full enumeration: same optimal cost and
// same tie-broken assignment.
func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	teams := []string{"A", "B", "C"}
	for iter := 0; iter < 300; iter++ {
		sizes := []int{2, 4, 8}
		size := sizes[rng.Intn(len(sizes))]
		n := 2 + rng.Intn(size-1) // 2..size players
		players := make([]Player, n)
		for i := range players {
			players[i] = Player{ID: i + 1, Team: teams[rng.Intn(len(teams))]}
		}
		cfg := &Config{Size: size, Players: players}

		// Half the time add a tier partition consistent with the players.
		if rng.Intn(2) == 0 {
			tiers := make([]int, n)
			for i := range tiers {
				tiers[i] = 1 + rng.Intn(2)
				players[i].Tier = tiers[i]
			}
			slotTiers := append([]int(nil), tiers...)
			for len(slotTiers) < size {
				slotTiers = append(slotTiers, 1+rng.Intn(2))
				// keep counts consistent: extend players instead
			}
			// Simpler: derive slot tiers from a shuffled copy of player
			// tiers, padding with byes only when there is no tiering.
			slotTiers = append([]int(nil), tiers...)
			rng.Shuffle(len(slotTiers), func(i, j int) { slotTiers[i], slotTiers[j] = slotTiers[j], slotTiers[i] })
			if len(slotTiers) == size {
				cfg.SlotTiers = slotTiers
			} else {
				// byes present: only tier when sizes match exactly
				if n == size {
					cfg.SlotTiers = slotTiers
				}
			}
		}

		// Lock up to two slots to distinct players (tier-compatible).
		if p, err := NewProblem(cfg); err == nil {
			_ = p
			locks := rng.Intn(3)
			usedSlots := map[int]bool{}
			usedPlayers := map[int]bool{}
			cfg.Locked = map[int]int{}
			for k := 0; k < locks; k++ {
				s := rng.Intn(size)
				pi := rng.Intn(n)
				if usedSlots[s] || usedPlayers[pi] {
					continue
				}
				if cfg.SlotTiers != nil && cfg.SlotTiers[s] != players[pi].Tier {
					continue
				}
				usedSlots[s] = true
				usedPlayers[pi] = true
				cfg.Locked[s] = players[pi].ID
			}
		}

		p, err := NewProblem(cfg)
		if err != nil {
			t.Fatalf("iter %d: NewProblem: %v", iter, err)
		}
		sol := p.Solve()
		bf, _, ok := p.BruteForce(1 << 60)
		if !ok {
			t.Fatalf("iter %d: brute force aborted", iter)
		}
		if sol.cost != bf.cost {
			t.Fatalf("iter %d: solve cost %d != brute cost %d (cfg %+v)", iter, sol.cost, bf.cost, *cfg)
		}
		if p.prefixCmp(sol.assign, bf.assign, p.size) != 0 {
			t.Fatalf("iter %d: tie-break mismatch solve=%v brute=%v", iter, sol.assign, bf.assign)
		}
		if got := p.Cost(sol); got != sol.cost {
			t.Fatalf("iter %d: recomputed cost %d != %d", iter, got, sol.cost)
		}
	}
}

func TestPairReports(t *testing.T) {
	cfg := &Config{
		Size: 8,
		Players: []Player{
			{ID: 1, Team: "A"}, {ID: 5, Team: "A"},
			{ID: 2, Team: "B"}, {ID: 6, Team: "B"},
		},
	}
	p := mustProblem(t, cfg)
	sol := p.Solve()
	reps := p.PairReports(sol)
	if len(reps) != 2 {
		t.Fatalf("got %d pair reports, want 2", len(reps))
	}
	var sum int64
	for _, r := range reps {
		if r.Round != p.rounds {
			t.Errorf("pair %+v meets in round %d, want final round %d", r, r.Round, p.rounds)
		}
		sum += r.Penalty
	}
	if sum != sol.cost {
		t.Errorf("pair penalties sum %d != total cost %d", sum, sol.cost)
	}
}
