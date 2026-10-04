package main

import (
	"fmt"
	"math/rand"
	"testing"
)

// bruteForce enumerates all subsets and returns the optimal total value,
// evaluating synergies strictly as pairwise terms.
func bruteForce(t *testing.T, in Input) int64 {
	t.Helper()
	s, err := NewSolver(in)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	n := s.n
	best := int64(0)
	for mask := uint64(0); mask < uint64(1)<<uint(n); mask++ {
		cost := int64(0)
		ok := true
		for i := 0; i < n && ok; i++ {
			if mask>>uint(i)&1 == 0 {
				continue
			}
			cost += s.cost[i]
			if s.prereq[i]&^mask != 0 || s.exclude[i]&mask != 0 {
				ok = false
			}
		}
		if !ok || cost > s.budget {
			continue
		}
		val := int64(0)
		for i := 0; i < n; i++ {
			if mask>>uint(i)&1 == 1 {
				val += s.value[i]
			}
		}
		for i := 0; i < n; i++ {
			if mask>>uint(i)&1 == 0 {
				continue
			}
			for j := i + 1; j < n; j++ {
				if mask>>uint(j)&1 == 1 {
					val += s.syn[i*n+j]
				}
			}
		}
		if val > best {
			best = val
		}
	}
	return best
}

func solveTotal(t *testing.T, in Input) Result {
	t.Helper()
	s, err := NewSolver(in)
	if err != nil {
		t.Fatalf("NewSolver: %v", err)
	}
	res := s.Solve()
	if res.Total != res.StandaloneSum+res.SynergySum {
		t.Fatalf("decomposition mismatch: %d != %d + %d", res.Total, res.StandaloneSum, res.SynergySum)
	}
	if res.TotalCost > in.Budget {
		t.Fatalf("cost %d exceeds budget %d", res.TotalCost, in.Budget)
	}
	if got := recomputeTotal(s, res.Selected); got != res.Total {
		t.Fatalf("recompute mismatch: %d != %d", got, res.Total)
	}
	if res.RootBound < res.Total {
		t.Fatalf("root bound %d below optimum %d: bound is invalid", res.RootBound, res.Total)
	}
	return res
}

func TestNegativeSynergyAvoided(t *testing.T) {
	in := Input{
		Budget: 100,
		Projects: []Project{
			{Name: "A", Cost: 10, Value: 50},
			{Name: "B", Cost: 10, Value: 50},
			{Name: "C", Cost: 10, Value: 20},
		},
		Synergies: []Synergy{{A: "A", B: "B", Value: -80}},
	}
	res := solveTotal(t, in)
	// A+B together is worth 20; A+C or B+C is worth 70.
	if res.Total != 70 {
		t.Fatalf("got %d, want 70 (selection %v)", res.Total, res.Selected)
	}
	if res.SynergySum != 0 {
		t.Fatalf("negative synergy should not be active, got synergy sum %d", res.SynergySum)
	}
}

func TestNegativeSynergyAcceptedWhenForced(t *testing.T) {
	in := Input{
		Budget: 20,
		Projects: []Project{
			{Name: "A", Cost: 10, Value: 60},
			{Name: "B", Cost: 10, Value: 60, Requires: []string{"A"}},
		},
		Synergies: []Synergy{{A: "A", B: "B", Value: -30}},
	}
	res := solveTotal(t, in)
	// A alone = 60; A+B = 120-30 = 90. Negative synergy must be paid.
	if res.Total != 90 || res.SynergySum != -30 {
		t.Fatalf("got total %d synergy %d, want 90 / -30", res.Total, res.SynergySum)
	}
}

func TestPrerequisiteAndExclusion(t *testing.T) {
	in := Input{
		Budget: 30,
		Projects: []Project{
			{Name: "core", Cost: 5, Value: 1},
			{Name: "app", Cost: 10, Value: 40, Requires: []string{"core"}},
			{Name: "alt", Cost: 10, Value: 40, Excludes: []string{"app"}},
		},
	}
	res := solveTotal(t, in)
	// app needs core: 15 cost, 41 value; alt alone: 10 cost, 40 value.
	// app+alt forbidden. Best: core+app = 41.
	if res.Total != 41 {
		t.Fatalf("got %d (%v), want 41", res.Total, res.Selected)
	}
}

func TestEmptyWhenAllNegative(t *testing.T) {
	in := Input{
		Budget: 10,
		Projects: []Project{
			{Name: "A", Cost: 5, Value: -3},
			{Name: "B", Cost: 5, Value: -4},
		},
		Synergies: []Synergy{{A: "A", B: "B", Value: -10}},
	}
	res := solveTotal(t, in)
	if res.Total != 0 || len(res.Selected) != 0 {
		t.Fatalf("got %d %v, want empty selection worth 0", res.Total, res.Selected)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []Input{
		{Budget: 1, Projects: []Project{{Name: "A", Cost: 1, Value: 1, Requires: []string{"A"}}}},
		{Budget: 1, Projects: []Project{
			{Name: "A", Cost: 1, Value: 1, Requires: []string{"B"}},
			{Name: "B", Cost: 1, Value: 1, Requires: []string{"A"}},
		}},
		{Budget: 1, Projects: []Project{{Name: "A", Cost: 1, Value: 1, Requires: []string{"ZZZ"}}}},
		{Budget: 1, Projects: []Project{{Name: "A", Cost: -1, Value: 1}}},
		{Budget: 1, Projects: []Project{{Name: "A", Cost: 1, Value: 1}, {Name: "A", Cost: 1, Value: 1}}},
	}
	for k, in := range cases {
		if _, err := NewSolver(in); err == nil {
			t.Fatalf("case %d: expected validation error", k)
		}
	}
	tooMany := Input{Budget: 1}
	for i := 0; i < 27; i++ {
		tooMany.Projects = append(tooMany.Projects, Project{Name: fmt.Sprintf("p%d", i), Cost: 1, Value: 1})
	}
	if _, err := NewSolver(tooMany); err == nil {
		t.Fatal("expected error for 27 projects")
	}
}

func TestBruteForceCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(11) // 2..12 projects
		in := Input{Budget: int64(5 + rng.Intn(40))}
		for i := 0; i < n; i++ {
			in.Projects = append(in.Projects, Project{
				Name:  fmt.Sprintf("p%d", i),
				Cost:  int64(rng.Intn(15)),
				Value: int64(rng.Intn(41) - 10),
			})
		}
		// Random acyclic prerequisites: i may only require j < i.
		for i := 1; i < n; i++ {
			for j := 0; j < i; j++ {
				if rng.Intn(10) == 0 {
					in.Projects[i].Requires = append(in.Projects[i].Requires, fmt.Sprintf("p%d", j))
				}
			}
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Intn(15) == 0 {
					in.Projects[i].Excludes = append(in.Projects[i].Excludes, fmt.Sprintf("p%d", j))
				}
				if rng.Intn(4) == 0 {
					in.Synergies = append(in.Synergies, Synergy{
						A:     fmt.Sprintf("p%d", i),
						B:     fmt.Sprintf("p%d", j),
						Value: int64(rng.Intn(61) - 30), // negative synergies included
					})
				}
			}
		}
		if _, err := NewSolver(in); err != nil {
			continue // requires+excludes conflict etc.; skip invalid instances
		}
		want := bruteForce(t, in)
		res := solveTotal(t, in)
		if res.Total != want {
			t.Fatalf("trial %d: solver=%d brute=%d\ninstance: %+v", trial, res.Total, want, in)
		}
	}
}
