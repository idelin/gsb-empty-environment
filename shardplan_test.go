package shardplan

import (
	"math"
	"testing"
)

func almostEq(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*(1+math.Abs(b))
}

func TestBasicBalancing(t *testing.T) {
	tests := []Test{
		{Name: "a", Duration: 1, Previous: -1},
		{Name: "b", Duration: 2, Previous: -1},
		{Name: "c", Duration: 3, Previous: -1},
		{Name: "d", Duration: 4, Previous: -1},
	}
	r, err := Plan(tests, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Feasible {
		t.Fatal("expected feasible")
	}
	if r.Migrations != 0 {
		t.Fatalf("expected 0 migrations, got %d", r.Migrations)
	}
	if !almostEq(r.MaxLoad, 5) {
		t.Fatalf("expected max load 5, got %v", r.MaxLoad)
	}
}

func TestForcedMigrations(t *testing.T) {
	// Four tests of duration 2, all previously on executor 0; each
	// executor fits two, so exactly two must migrate.
	var tests []Test
	for _, n := range []string{"a", "b", "c", "d"} {
		tests = append(tests, Test{Name: n, Duration: 2, Previous: 0})
	}
	r, err := Plan(tests, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Feasible {
		t.Fatal("expected feasible")
	}
	if r.Migrations != 2 {
		t.Fatalf("expected 2 migrations, got %d", r.Migrations)
	}
	if !almostEq(r.MaxLoad, 4) {
		t.Fatalf("expected max load 4, got %v", r.MaxLoad)
	}
}

func TestMigrationsBeforeMaxLoad(t *testing.T) {
	// Keeping t1 on executor 0 yields max load 8 with 0 migrations;
	// moving it would balance to 5/5 but cost a migration. Migrations
	// must win.
	tests := []Test{
		{Name: "t0", Duration: 5, Previous: 0},
		{Name: "t1", Duration: 3, Previous: 0},
		{Name: "t2", Duration: 2, Previous: 1},
	}
	r, err := Plan(tests, 2, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Feasible {
		t.Fatal("expected feasible")
	}
	if r.Migrations != 0 {
		t.Fatalf("expected 0 migrations, got %d", r.Migrations)
	}
	if !almostEq(r.MaxLoad, 8) {
		t.Fatalf("expected max load 8, got %v", r.MaxLoad)
	}
}

func TestMaxLoadTieBreak(t *testing.T) {
	// New test t2 can go on either executor without migrating; the
	// planner should place it to minimize the max load (3 vs 5).
	tests := []Test{
		{Name: "t0", Duration: 3, Previous: 0},
		{Name: "t1", Duration: 4, Previous: 1},
		{Name: "t2", Duration: 2, Previous: -1},
	}
	r, err := Plan(tests, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Feasible {
		t.Fatal("expected feasible")
	}
	if r.Migrations != 0 {
		t.Fatalf("expected 0 migrations, got %d", r.Migrations)
	}
	if !almostEq(r.MaxLoad, 5) {
		t.Fatalf("expected max load 5, got %v", r.MaxLoad)
	}
	if r.Assignments["t2"] != 0 {
		t.Fatalf("expected t2 on executor 0, got %d", r.Assignments["t2"])
	}
}

func TestGroupCoLocation(t *testing.T) {
	tests := []Test{
		{Name: "a", Duration: 2, Previous: -1},
		{Name: "g1", Duration: 2, Group: "g", Previous: -1},
		{Name: "g2", Duration: 2, Group: "g", Previous: -1},
		{Name: "b", Duration: 2, Previous: -1},
	}
	r, err := Plan(tests, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Feasible {
		t.Fatal("expected feasible")
	}
	if r.Assignments["g1"] != r.Assignments["g2"] {
		t.Fatal("group members must share an executor")
	}
	if !almostEq(r.MaxLoad, 4) {
		t.Fatalf("expected max load 4, got %v", r.MaxLoad)
	}
}

func TestGroupSplitPrevious(t *testing.T) {
	// A group whose members were previously split across executors must
	// reunite; exactly one migration is unavoidable.
	tests := []Test{
		{Name: "g1", Duration: 1, Group: "g", Previous: 0},
		{Name: "g2", Duration: 1, Group: "g", Previous: 1},
	}
	r, err := Plan(tests, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if r.Migrations != 1 {
		t.Fatalf("expected 1 migration, got %d", r.Migrations)
	}
	if r.Assignments["g1"] != r.Assignments["g2"] {
		t.Fatal("group members must share an executor")
	}
}

func TestInfeasibleLowerBound(t *testing.T) {
	tests := []Test{
		{Name: "a", Duration: 5, Previous: -1},
		{Name: "b", Duration: 5, Previous: -1},
		{Name: "c", Duration: 5, Previous: -1},
	}
	r, err := Plan(tests, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	if r.Feasible {
		t.Fatal("expected infeasible")
	}
	if !almostEq(r.LowerBound, 10) {
		t.Fatalf("expected lower bound 10, got %v", r.LowerBound)
	}
	if !almostEq(r.MaxLoad, r.LowerBound) {
		t.Fatalf("suggested plan should attain the lower bound: %v vs %v", r.MaxLoad, r.LowerBound)
	}
}

func TestSingleExecutor(t *testing.T) {
	tests := []Test{
		{Name: "a", Duration: 3, Previous: 0},
		{Name: "b", Duration: 4, Previous: 0},
	}
	r, err := Plan(tests, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Feasible || !almostEq(r.MaxLoad, 7) || r.Migrations != 0 {
		t.Fatalf("unexpected result: %+v", r)
	}
	r, err = Plan(tests, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if r.Feasible || !almostEq(r.LowerBound, 7) {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestValidation(t *testing.T) {
	ok := []Test{{Name: "a", Duration: 1, Previous: -1}}
	if _, err := Plan(ok, 0, 1); err == nil {
		t.Fatal("expected error for 0 executors")
	}
	if _, err := Plan(ok, 1, -1); err == nil {
		t.Fatal("expected error for negative capacity")
	}
	if _, err := Plan([]Test{{Name: "a", Duration: 1, Previous: 3}}, 2, 1); err == nil {
		t.Fatal("expected error for out-of-range previous executor")
	}
	if _, err := Plan([]Test{{Name: "a", Duration: 1, Previous: -1}, {Name: "a", Duration: 1, Previous: -1}}, 2, 1); err == nil {
		t.Fatal("expected error for duplicate names")
	}
	many := make([]Test, MaxTests+1)
	for i := range many {
		many[i] = Test{Name: string(rune('a'+i%26)) + string(rune('A'+i/26)), Duration: 1, Previous: -1}
	}
	if _, err := Plan(many, 2, 100); err == nil {
		t.Fatal("expected error for more than MaxTests tests")
	}
}

func TestEmpty(t *testing.T) {
	r, err := Plan(nil, 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Feasible || r.Migrations != 0 || r.MaxLoad != 0 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

// bruteForce enumerates all assignments and returns the optimal
// (feasible, migrations, maxLoad) under the capacity, plus the optimal
// unconstrained makespan.
func bruteForce(tests []Test, k int, cap float64) (feasible bool, moves int, maxLoad, makespan float64) {
	items := buildItems(tests, k)
	loads := make([]float64, k)
	place := make([]int, len(items))
	bestMoves := int(1 << 30)
	bestMax := math.Inf(1)
	makespan = math.Inf(1)
	var rec func(i int)
	rec = func(i int) {
		if i == len(items) {
			m := 0
			mx := 0.0
			for e := 0; e < k; e++ {
				if loads[e] > mx {
					mx = loads[e]
				}
			}
			for j, it := range items {
				for _, ti := range it.tests {
					if tests[ti].Previous >= 0 && tests[ti].Previous != place[j] {
						m++
					}
				}
			}
			if mx < makespan {
				makespan = mx
			}
			if mx <= cap+1e-9*(1+cap) {
				if m < bestMoves || (m == bestMoves && mx < bestMax) {
					bestMoves, bestMax = m, mx
				}
			}
			return
		}
		for e := 0; e < k; e++ {
			loads[e] += items[i].dur
			place[i] = e
			rec(i + 1)
			loads[e] -= items[i].dur
		}
	}
	rec(0)
	return bestMoves < int(1<<30), bestMoves, bestMax, makespan
}

// lcg is a small deterministic PRNG for reproducible tests.
type lcg struct{ s uint64 }

func (l *lcg) next() uint64 {
	l.s = l.s*6364136223846793005 + 1442695040888963407
	return l.s >> 33
}

func (l *lcg) intn(n int) int { return int(l.next() % uint64(n)) }

func TestAgainstBruteForce(t *testing.T) {
	rng := &lcg{s: 42}
	for iter := 0; iter < 300; iter++ {
		k := 2 + rng.intn(2) // 2..3 executors
		n := 1 + rng.intn(7) // 1..7 tests
		var tests []Test
		for i := 0; i < n; i++ {
			tt := Test{
				Name:     string(rune('a' + i)),
				Duration: float64(1 + rng.intn(6)),
				Previous: -1 + rng.intn(k+1), // -1..k-1
			}
			if rng.intn(3) == 0 {
				tt.Group = string(rune('A' + rune(rng.intn(2))))
			}
			tests = append(tests, tt)
		}
		total := 0.0
		for _, tt := range tests {
			total += tt.Duration
		}
		cap := total / float64(k) * (0.7 + 0.2*float64(rng.intn(4))) // 0.7..1.3 x avg
		r, err := Plan(tests, k, cap)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		wantFeasible, wantMoves, wantMax, wantMakespan := bruteForce(tests, k, cap)
		if r.Feasible != wantFeasible {
			t.Fatalf("iter %d: feasible=%v, want %v (tests=%v k=%d cap=%v)", iter, r.Feasible, wantFeasible, tests, k, cap)
		}
		if wantFeasible {
			if r.Migrations != wantMoves {
				t.Fatalf("iter %d: migrations=%d, want %d (tests=%v k=%d cap=%v)", iter, r.Migrations, wantMoves, tests, k, cap)
			}
			if !almostEq(r.MaxLoad, wantMax) {
				t.Fatalf("iter %d: maxLoad=%v, want %v (tests=%v k=%d cap=%v)", iter, r.MaxLoad, wantMax, tests, k, cap)
			}
			if r.MaxLoad > cap+1e-9*(1+cap) {
				t.Fatalf("iter %d: max load %v exceeds capacity %v", iter, r.MaxLoad, cap)
			}
		} else if !almostEq(r.LowerBound, wantMakespan) {
			t.Fatalf("iter %d: lowerBound=%v, want %v (tests=%v k=%d cap=%v)", iter, r.LowerBound, wantMakespan, tests, k, cap)
		}
		// Group co-location must always hold.
		groupExec := map[string]int{}
		for _, tt := range tests {
			if tt.Group == "" {
				continue
			}
			if e, ok := groupExec[tt.Group]; ok && e != r.Assignments[tt.Name] {
				t.Fatalf("iter %d: group %q split across executors", iter, tt.Group)
			}
			groupExec[tt.Group] = r.Assignments[tt.Name]
		}
	}
}

func TestStress24(t *testing.T) {
	rng := &lcg{s: 7}
	for iter := 0; iter < 20; iter++ {
		k := 2 + rng.intn(4) // 2..5 executors
		var tests []Test
		total := 0.0
		for i := 0; i < MaxTests; i++ {
			d := float64(1 + rng.intn(100))
			total += d
			tt := Test{
				Name:     string(rune('a'+i%26)) + string(rune('A'+i/26)),
				Duration: d,
				Previous: -1 + rng.intn(k+1),
			}
			if rng.intn(4) == 0 {
				tt.Group = string(rune('A' + rune(rng.intn(3))))
			}
			tests = append(tests, tt)
		}
		cap := total / float64(k) * 1.15
		r, err := Plan(tests, k, cap)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		if r.Feasible && r.MaxLoad > cap+1e-9*(1+cap) {
			t.Fatalf("iter %d: max load %v exceeds capacity %v", iter, r.MaxLoad, cap)
		}
		if !r.Feasible && r.LowerBound < total/float64(k)-1e-6 {
			t.Fatalf("iter %d: lower bound %v below average load %v", iter, r.LowerBound, total/float64(k))
		}
	}
}
