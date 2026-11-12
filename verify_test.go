package netsec

import (
	"math/rand"
	"testing"
)

func TestVerifyDeadEndsConsistency(t *testing.T) {
	m := Model{
		Regions:       3,
		Target:        0b101010,
		TargetMask:    0b111111,
		ForbiddenMask: 1 << EnableBit(2),
	}
	report := VerifyDeadEnds(m, DefaultVerifyStates)
	if report.Err != nil {
		t.Fatal(report.Err)
	}
	if !report.Consistent {
		t.Fatalf("algorithms disagree: %+v", report)
	}
	if report.Space != 1<<6 || report.Safe != 1<<5 {
		t.Fatalf("space=%d safe=%d", report.Space, report.Safe)
	}
	if !report.GoalReachable {
		t.Fatal("goal should be reachable from zero")
	}
	if report.Dead == 0 {
		t.Fatal("expected dead configurations")
	}

	p := PlanModel(m, Budget{})
	if p.Status != StatusSolved {
		t.Fatalf("planner status=%s", p.Status)
	}
}

func TestVerifyRejectsLargeSpace(t *testing.T) {
	m := Model{Regions: 10, TargetMask: 1}
	report := VerifyDeadEnds(m, 1024)
	if report.Err == nil {
		t.Fatal("expected budget error")
	}
}

// TestPredecessorsInverse checks that every emitted predecessor really can
// reach the state in one legal Apply step, and that no safe predecessor is
// missed.
func TestPredecessorsInverse(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 200; iter++ {
		regions := 1 + rng.Intn(3)
		m := Model{Regions: regions}
		for r := 0; r < regions; r++ {
			if rng.Intn(3) == 0 {
				m.ForbiddenMask |= 1 << EnableBit(r)
			}
		}
		state := rng.Uint32() & m.fullMask()
		if !m.Safe(state) {
			continue
		}

		got := map[uint32]bool{}
		m.Predecessors(state, func(prev uint32) bool {
			got[prev] = true
			return true
		})

		// Reference: every safe distinct state other than state that can reach
		// it through the real Apply transition semantics.
		for prev := uint32(0); prev <= m.fullMask(); prev++ {
			if !m.Safe(prev) || prev == state {
				continue
			}
			reachable := canReachInOne(m, prev, state)
			if reachable != got[prev] {
				t.Fatalf("iter %d: predecessor %b -> %b mismatch (got=%v want=%v)",
					iter, prev, state, got[prev], reachable)
			}
		}
	}
}

func canReachInOne(m Model, prev, state uint32) bool {
	full := m.fullMask()
	for mask := uint32(1); mask <= full; mask++ {
		for value := uint32(0); value <= full; value++ {
			if Apply(prev, Write{WriteMask: mask, Value: value}, m.Regions) == state {
				return true
			}
		}
	}
	return false
}

// TestRandomizedPlans cross-checks the planner against an independent BFS that
// derives successors by enumerating raw (mask, value) transactions.
func TestRandomizedPlans(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 60; iter++ {
		regions := 1 + rng.Intn(3)
		m := Model{
			Regions:     regions,
			SingleField: rng.Intn(2) == 0,
		}
		full := m.fullMask()
		m.Start = rng.Uint32() & full
		m.TargetMask = rng.Uint32() & full
		m.Target = rng.Uint32() & m.TargetMask

		for r := 0; r < regions; r++ {
			if rng.Intn(3) == 0 {
				m.ForbiddenMask |= 1 << EnableBit(r)
			}
		}
		m.Target &^= m.ForbiddenMask
		if err := m.Validate(); err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}

		brute := bruteForceDepth(m)
		p := PlanModel(m, Budget{MaxVisited: 1 << 20})

		switch brute {
		case -1:
			if p.Status != StatusUnreachable {
				t.Fatalf("iter %d: want unreachable, got %s (%+v)", iter, p.Status, p.Err)
			}
		default:
			if p.Status != StatusSolved || p.Depth != brute {
				t.Fatalf("iter %d: plan status=%s depth=%d brute=%d", iter, p.Status, p.Depth, brute)
			}
			final := simulateWrites(t, m, p.Writes)
			if !m.AtGoal(final) {
				t.Fatalf("iter %d: final %b misses goal", iter, final)
			}
		}
	}
}

func bruteForceDepth(m Model) int {
	if !m.Safe(m.Start) {
		return -1
	}
	if m.AtGoal(m.Start) {
		return 0
	}
	full := m.fullMask()
	depth := map[uint32]int{m.Start: 0}
	frontier := []uint32{m.Start}
	transactions := allTransactions(m, full)
	for len(frontier) > 0 {
		state := frontier[0]
		frontier = frontier[1:]
		nextDepth := depth[state] + 1

		for _, w := range transactions {
			next := Apply(state, w, m.Regions)
			if next == state || !m.Safe(next) {
				continue
			}
			if _, ok := depth[next]; ok {
				continue
			}
			depth[next] = nextDepth
			if m.AtGoal(next) {
				return nextDepth
			}
			frontier = append(frontier, next)
		}
	}
	return -1
}

func allTransactions(m Model, full uint32) []Write {
	if !m.SingleField {
		var out []Write
		for mask := uint32(1); mask <= full; mask++ {
			for value := uint32(0); value <= full; value++ {
				out = append(out, Write{WriteMask: mask, Value: value})
			}
		}
		return out
	}
	var out []Write
	for r := 0; r < m.Regions; r++ {
		regionMask := uint32(1)<<EnableBit(r) | uint32(1)<<LockBit(r)
		for sub := uint32(1); sub <= regionMask; sub = (sub - regionMask) & regionMask {
			for value := uint32(0); value <= regionMask; value++ {
				if value&^sub != 0 {
					continue
				}
				out = append(out, Write{WriteMask: sub, Value: value})
			}
			if sub == regionMask {
				break
			}
		}
	}
	return out
}

func TestTwentyFourBitField(t *testing.T) {
	m := Model{
		Regions:    12,
		Target:     mLockAll(12),
		TargetMask: mLockAll(12),
	}
	p := PlanModel(m, Budget{})
	if p.Status != StatusSolved || p.Depth != 1 {
		t.Fatalf("status=%s depth=%d err=%v", p.Status, p.Depth, p.Err)
	}
	final := simulateWrites(t, m, p.Writes)
	if !m.AtGoal(final) {
		t.Fatalf("final %b misses goal", final)
	}
}

func mLockAll(regions int) uint32 {
	var mask uint32
	for r := 0; r < regions; r++ {
		mask |= 1 << LockBit(r)
	}
	return mask
}
