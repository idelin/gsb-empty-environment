package shardplan

import (
	"math"
	"math/rand"
	"testing"
)

func TestMigrationsBeforeBalance(t *testing.T) {
	// Staying put gives max load 10 with 0 migrations; rebalancing to
	// (6, 8) would need 1 migration. Migrations win.
	res, err := Plan([]Test{
		{Name: "a", Duration: 6, Executor: 0},
		{Name: "b", Duration: 4, Executor: 0},
		{Name: "c", Duration: 4, Executor: 1},
	}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible {
		t.Fatalf("expected feasible, floor=%d", res.CapacityFloor)
	}
	if res.Migrations != 0 || res.MaxLoad != 10 {
		t.Fatalf("got migrations=%d maxLoad=%d, want 0 and 10", res.Migrations, res.MaxLoad)
	}
	if res.Assignment["a"] != 0 || res.Assignment["b"] != 0 || res.Assignment["c"] != 1 {
		t.Fatalf("unexpected assignment: %v", res.Assignment)
	}
}

func TestMinMaxLoadAmongMinMigrations(t *testing.T) {
	// Two plans with 1 migration exist; the one with max load 10 wins.
	res, err := Plan([]Test{
		{Name: "a", Duration: 8, Executor: 0},
		{Name: "b", Duration: 8, Executor: 0},
		{Name: "c", Duration: 2, Executor: 1},
	}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible || res.Migrations != 1 || res.MaxLoad != 10 {
		t.Fatalf("got feasible=%v migrations=%d maxLoad=%d", res.Feasible, res.Migrations, res.MaxLoad)
	}
	if res.Assignment["a"] == res.Assignment["b"] {
		t.Fatalf("a and b cannot share an executor under limit 10: %v", res.Assignment)
	}
}

func TestGroupCoLocation(t *testing.T) {
	res, err := Plan([]Test{
		{Name: "a", Duration: 3, Executor: 0, Group: "g"},
		{Name: "b", Duration: 3, Executor: 1, Group: "g"},
		{Name: "c", Duration: 4, Executor: 1},
	}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible || res.Migrations != 1 || res.MaxLoad != 6 {
		t.Fatalf("got feasible=%v migrations=%d maxLoad=%d", res.Feasible, res.Migrations, res.MaxLoad)
	}
	if res.Assignment["a"] != res.Assignment["b"] {
		t.Fatalf("group split across executors: %v", res.Assignment)
	}
	if res.Assignment["a"] != 0 || res.Assignment["c"] != 1 {
		t.Fatalf("unexpected assignment: %v", res.Assignment)
	}
}

func TestInfeasibleReportsCapacityFloor(t *testing.T) {
	res, err := Plan([]Test{
		{Name: "a", Duration: 8, Executor: 0},
		{Name: "b", Duration: 8, Executor: 0},
		{Name: "c", Duration: 2, Executor: 1},
	}, 2, 9)
	if err != nil {
		t.Fatal(err)
	}
	if res.Feasible {
		t.Fatal("expected infeasible")
	}
	if res.CapacityFloor != 10 {
		t.Fatalf("capacity floor = %d, want 10", res.CapacityFloor)
	}
	if res.Assignment != nil {
		t.Fatalf("infeasible result must not carry an assignment: %v", res.Assignment)
	}
}

func TestCapacityFloorFromSingleTest(t *testing.T) {
	// One huge test dominates: the floor is its own duration.
	res, err := Plan([]Test{
		{Name: "big", Duration: 100, Executor: Unassigned},
		{Name: "s1", Duration: 3, Executor: Unassigned},
		{Name: "s2", Duration: 3, Executor: Unassigned},
	}, 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	if res.Feasible || res.CapacityFloor != 100 {
		t.Fatalf("got feasible=%v floor=%d, want false and 100", res.Feasible, res.CapacityFloor)
	}
}

func TestUnassignedTestsAreNotMigrations(t *testing.T) {
	res, err := Plan([]Test{
		{Name: "old", Duration: 5, Executor: 0},
		{Name: "new", Duration: 5, Executor: Unassigned},
	}, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible || res.Migrations != 0 {
		t.Fatalf("got feasible=%v migrations=%d", res.Feasible, res.Migrations)
	}
}

func TestTooManyTests(t *testing.T) {
	tests := make([]Test, MaxTests+1)
	for i := range tests {
		tests[i] = Test{Name: string(rune('a' + i)), Duration: 1, Executor: 0}
	}
	if _, err := Plan(tests, 2, 100); err == nil {
		t.Fatal("expected error for more than MaxTests tests")
	}
}

func TestValidation(t *testing.T) {
	good := []Test{{Name: "a", Duration: 1, Executor: 0}}
	if _, err := Plan(nil, 2, 1); err == nil {
		t.Fatal("empty tests: expected error")
	}
	if _, err := Plan(good, 0, 1); err == nil {
		t.Fatal("zero executors: expected error")
	}
	if _, err := Plan([]Test{{Name: "a", Duration: -1, Executor: 0}}, 1, 1); err == nil {
		t.Fatal("negative duration: expected error")
	}
	if _, err := Plan([]Test{{Name: "a", Duration: 1, Executor: 0}, {Name: "a", Duration: 1, Executor: 0}}, 1, 2); err == nil {
		t.Fatal("duplicate name: expected error")
	}
	if _, err := Plan([]Test{{Name: "a", Duration: 1, Executor: 5}}, 2, 2); err == nil {
		t.Fatal("executor out of range: expected error")
	}
}

// bruteForce enumerates every item assignment and returns the best
// (migrations, maxLoad) within limit, plus the unconstrained minimum
// maxLoad (the capacity floor).
func bruteForce(tests []Test, k int, limit int64) (mig int, bestMax int64, floor int64, feasible bool) {
	items := buildItems(append([]Test(nil), tests...))
	mig, bestMax, floor = math.MaxInt, int64(math.MaxInt64), int64(math.MaxInt64)
	loads := make([]int64, k)
	assign := make([]int, len(items))
	var rec func(i int)
	rec = func(i int) {
		if i == len(items) {
			m := maxLoad(loads)
			if m < floor {
				floor = m
			}
			if m <= limit {
				c := 0
				for j, it := range items {
					c += it.migCost(assign[j])
				}
				if c < mig || (c == mig && m < bestMax) {
					mig, bestMax, feasible = c, m, true
				}
			}
			return
		}
		for e := 0; e < k; e++ {
			assign[i] = e
			loads[e] += items[i].dur
			rec(i + 1)
			loads[e] -= items[i].dur
		}
	}
	rec(0)
	return
}

func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 2000; trial++ {
		n := 1 + rng.Intn(8)
		k := 1 + rng.Intn(3)
		tests := make([]Test, n)
		for i := range tests {
			ex := Unassigned
			if rng.Intn(4) > 0 {
				ex = rng.Intn(k)
			}
			g := ""
			if rng.Intn(3) == 0 {
				g = string(rune('A' + rng.Intn(3)))
			}
			tests[i] = Test{
				Name:     string(rune('a'+i)) + string(rune('0'+trial%10)),
				Duration: int64(1 + rng.Intn(9)),
				Executor: ex,
				Group:    g,
			}
		}
		limit := int64(1 + rng.Intn(30))
		res, err := Plan(tests, k, limit)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		wantMig, wantMax, wantFloor, wantFeasible := bruteForce(tests, k, limit)
		if res.CapacityFloor != wantFloor {
			t.Fatalf("trial %d: floor=%d, want %d (tests=%v k=%d)", trial, res.CapacityFloor, wantFloor, tests, k)
		}
		if res.Feasible != wantFeasible {
			t.Fatalf("trial %d: feasible=%v, want %v (limit=%d floor=%d)", trial, res.Feasible, wantFeasible, limit, wantFloor)
		}
		if !wantFeasible {
			continue
		}
		if res.Migrations != wantMig || res.MaxLoad != wantMax {
			t.Fatalf("trial %d: got (mig=%d, max=%d), want (%d, %d) (tests=%v k=%d limit=%d)",
				trial, res.Migrations, res.MaxLoad, wantMig, wantMax, tests, k, limit)
		}
		// Verify the returned assignment itself.
		loads := make([]int64, k)
		groupAt := map[string]int{}
		for _, tc := range tests {
			e, ok := res.Assignment[tc.Name]
			if !ok || e < 0 || e >= k {
				t.Fatalf("trial %d: bad assignment for %q", trial, tc.Name)
			}
			loads[e] += tc.Duration
			if tc.Group != "" {
				if prev, seen := groupAt[tc.Group]; seen && prev != e {
					t.Fatalf("trial %d: group %q split", trial, tc.Group)
				}
				groupAt[tc.Group] = e
			}
		}
		if m := maxLoad(loads); m != res.MaxLoad || m > limit {
			t.Fatalf("trial %d: assignment max load %d, reported %d, limit %d", trial, m, res.MaxLoad, limit)
		}
		mig := 0
		for _, tc := range tests {
			if tc.Executor != Unassigned && res.Assignment[tc.Name] != tc.Executor {
				mig++
			}
		}
		if mig != res.Migrations {
			t.Fatalf("trial %d: assignment migrations %d, reported %d", trial, mig, res.Migrations)
		}
	}
}

func TestMaxSizeSmoke(t *testing.T) {
	// 24 tests must solve quickly.
	rng := rand.New(rand.NewSource(7))
	tests := make([]Test, MaxTests)
	for i := range tests {
		tests[i] = Test{Name: string(rune('a'+i/26)) + string(rune('a'+i%26)), Duration: int64(1 + rng.Intn(20)), Executor: rng.Intn(4)}
	}
	res, err := Plan(tests, 4, 80)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Feasible {
		t.Fatalf("expected feasible, floor=%d", res.CapacityFloor)
	}
}
