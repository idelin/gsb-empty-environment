package netsec_test

import (
	"math/rand"
	"testing"

	netsec "example.org/netsec/q0463"
)

func TestDifferentialRandomMachines(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 600; iter++ {
		m := randomMachine(rng, iter)

		sym, err := netsec.Explore(m)
		if err != nil {
			t.Fatalf("iter %d Explore: %v", iter, err)
		}
		ref, err := netsec.ReferenceExplore(m, 50000)
		if err != nil {
			t.Fatalf("iter %d ReferenceExplore: %v", iter, err)
		}
		if ref.Truncated {
			continue
		}
		if sym.Reachable != ref.Reachable {
			t.Fatalf("iter %d verdict mismatch: symbolic=%v reference=%v\nmachine=%+v",
				iter, sym.Reachable, ref.Reachable, m)
		}
		if sym.Reachable {
			if err := netsec.Replay(m, sym); err != nil {
				t.Fatalf("iter %d symbolic Replay: %v", iter, err)
			}
			if err := replayReference(m, ref); err != nil {
				t.Fatalf("iter %d reference Replay: %v", iter, err)
			}
			// 见证终点必须确实落在错误位置且满足其不变量。
			if sym.Target != ref.Target {
				t.Fatalf("iter %d target mismatch: %d vs %d", iter, sym.Target, ref.Target)
			}
		}
	}
}

func TestValidationEnforcesLimits(t *testing.T) {
	base := netsec.HandshakeMachine(false)

	badConst := *base
	badConst.Edges = append([]netsec.Edge(nil), base.Edges...)
	badConst.Edges[0].Guards = []netsec.Guard{{Clock: 0, Op: netsec.LE, Const: netsec.MaxConst + 1}}
	if _, err := netsec.Explore(&badConst); err == nil {
		t.Fatal("expected error for constant above MaxConst")
	}

	badClock := *base
	badClock.Clocks = netsec.MaxClocks + 1
	if _, err := netsec.Explore(&badClock); err == nil {
		t.Fatal("expected error for clock count above MaxClocks")
	}

	badInvariant := *base
	badInvariant.Invariants = append([][]netsec.Guard(nil), base.Invariants...)
	badInvariant.Invariants[netsec.LocIdle] = []netsec.Guard{{Clock: 0, Op: netsec.GE, Const: 1}}
	if _, err := netsec.Explore(&badInvariant); err == nil {
		t.Fatal("expected error for non-upper-bound invariant")
	}
}

func randomMachine(rng *rand.Rand, seed int) *netsec.Machine {
	clocks := 1 + rng.Intn(netsec.MaxClocks)
	locs := 1 + rng.Intn(4)
	ops := []netsec.Cmp{netsec.LE, netsec.LT, netsec.GE, netsec.GT, netsec.EQ}

	m := &netsec.Machine{
		Name:    "random",
		Clocks:  clocks,
		Initial: rng.Intn(locs),
	}
	for i := 0; i < locs; i++ {
		m.Locations = append(m.Locations, "L"+itoaTest(i))
	}
	m.Invariants = make([][]netsec.Guard, locs)
	for l := 0; l < locs; l++ {
		if rng.Intn(2) == 0 {
			c := rng.Intn(netsec.MaxConst + 1)
			op := netsec.LE
			if rng.Intn(2) == 0 {
				op = netsec.LT
			}
			m.Invariants[l] = []netsec.Guard{{Clock: rng.Intn(clocks), Op: op, Const: c}}
		}
	}

	edgeCount := 1 + rng.Intn(6)
	for e := 0; e < edgeCount; e++ {
		edge := netsec.Edge{
			From:  rng.Intn(locs),
			To:    rng.Intn(locs),
			Event: "e" + itoaTest(seed) + "_" + itoaTest(e),
		}
		guardCount := rng.Intn(3)
		for g := 0; g < guardCount; g++ {
			edge.Guards = append(edge.Guards, netsec.Guard{
				Clock: rng.Intn(clocks),
				Op:    ops[rng.Intn(len(ops))],
				Const: rng.Intn(netsec.MaxConst + 1),
			})
		}
		resetCount := rng.Intn(clocks + 1)
		used := map[int]bool{}
		for r := 0; r < resetCount; r++ {
			c := rng.Intn(clocks)
			if used[c] {
				continue
			}
			used[c] = true
			edge.Resets = append(edge.Resets, netsec.Action{Clock: c})
		}
		m.Edges = append(m.Edges, edge)
	}

	errLoc := rng.Intn(locs)
	if errLoc == m.Initial && rng.Intn(2) == 0 && locs > 1 {
		errLoc = (errLoc + 1) % locs
	}
	m.Errors = []int{errLoc}
	return m
}

func itoaTest(v int) string {
	if v == 0 {
		return "0"
	}
	out := ""
	for v > 0 {
		out = string(rune('0'+v%10)) + out
		v /= 10
	}
	return out
}
