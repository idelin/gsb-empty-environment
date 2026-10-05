package solver

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func solveErr(t *testing.T, p *Problem) error {
	t.Helper()
	_, err := Solve(p)
	return err
}

func mustSolve(t *testing.T, p *Problem) *Solution {
	t.Helper()
	sol, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return sol
}

func packagesOf(sol *Solution, material string) int64 {
	for _, p := range sol.Purchases {
		if p.Material == material {
			return p.Packages
		}
	}
	return 0
}

func applicationsOf(sol *Solution, rule string) int64 {
	for _, a := range sol.Applications {
		if a.Rule == rule {
			return a.Times
		}
	}
	return 0
}

func balanceOf(sol *Solution, material string) MaterialBalance {
	for _, b := range sol.Balances {
		if b.Material == material {
			return b
		}
	}
	return MaterialBalance{}
}

func TestDirectPurchaseRoundsUpToWholePackages(t *testing.T) {
	sol := mustSolve(t, &Problem{
		Materials: []Material{{Name: "b", PackageSize: 10, PackageCost: "3"}},
		Demands:   []Demand{{Material: "b", Quantity: 1}},
	})
	if sol.TotalCost != "3" || packagesOf(sol, "b") != 1 {
		t.Fatalf("got %+v", sol)
	}
	if bal := balanceOf(sol, "b"); bal.Leftover != 9 {
		t.Fatalf("leftover = %d, want 9", bal.Leftover)
	}
}

func TestMultiLevelSubstitutionChain(t *testing.T) {
	// Buying ore and converting twice is far cheaper than buying parts.
	sol := mustSolve(t, &Problem{
		Materials: []Material{
			{Name: "ore", PackageSize: 4, PackageCost: "6"},
			{Name: "ingot", PackageSize: 1, PackageCost: "100"},
			{Name: "part", PackageSize: 1, PackageCost: "100"},
		},
		Demands: []Demand{{Material: "part", Quantity: 3}},
		Rules: []Rule{
			{Name: "smelt", Input: Port{Material: "ore", Quantity: 1}, Output: Port{Material: "ingot", Quantity: 2}},
			{Name: "machine", Input: Port{Material: "ingot", Quantity: 1}, Output: Port{Material: "part", Quantity: 1}},
		},
	})
	if sol.TotalCost != "6" {
		t.Fatalf("total cost = %s, want 6", sol.TotalCost)
	}
	if packagesOf(sol, "ore") != 1 || applicationsOf(sol, "smelt") != 2 || applicationsOf(sol, "machine") != 3 {
		t.Fatalf("got %+v", sol)
	}
	if bal := balanceOf(sol, "ingot"); bal.Produced != 4 || bal.Consumed != 3 || bal.Leftover != 1 {
		t.Fatalf("ingot balance = %+v", bal)
	}
}

func TestAuxiliaryMaterialIsPurchasedAndCharged(t *testing.T) {
	sol := mustSolve(t, &Problem{
		Materials: []Material{
			{Name: "alloy", PackageSize: 2, PackageCost: "5"},
			{Name: "catalyst", PackageSize: 1, PackageCost: "1"},
			{Name: "widget", PackageSize: 1, PackageCost: "50"},
		},
		Demands: []Demand{{Material: "widget", Quantity: 2}},
		Rules: []Rule{
			{Name: "cast", Input: Port{Material: "alloy", Quantity: 1}, Output: Port{Material: "widget", Quantity: 1},
				Aux: []Port{{Material: "catalyst", Quantity: 1}}},
		},
	})
	if sol.TotalCost != "7" {
		t.Fatalf("total cost = %s, want 7 (5 alloy + 2 catalyst)", sol.TotalCost)
	}
	if packagesOf(sol, "catalyst") != 2 || applicationsOf(sol, "cast") != 2 {
		t.Fatalf("got %+v", sol)
	}
}

func TestRationalRatioSubstitution(t *testing.T) {
	// 2 a -> 3 b; demand 9 b needs 3 applications and 6 units of a,
	// i.e. 2 packages of 4 (integer rounding leaves 2 units over).
	sol := mustSolve(t, &Problem{
		Materials: []Material{
			{Name: "a", PackageSize: 4, PackageCost: "2"},
			{Name: "b", PackageSize: 1, PackageCost: "100"},
		},
		Demands: []Demand{{Material: "b", Quantity: 9}},
		Rules: []Rule{
			{Name: "conv", Input: Port{Material: "a", Quantity: 2}, Output: Port{Material: "b", Quantity: 3}},
		},
	})
	if sol.TotalCost != "4" || packagesOf(sol, "a") != 2 || applicationsOf(sol, "conv") != 3 {
		t.Fatalf("got %+v", sol)
	}
	if bal := balanceOf(sol, "a"); bal.Leftover != 2 {
		t.Fatalf("a leftover = %d, want 2", bal.Leftover)
	}
}

func TestGainCycleRejected(t *testing.T) {
	err := solveErr(t, &Problem{
		Materials: []Material{
			{Name: "a", PackageSize: 1, PackageCost: "1"},
			{Name: "b", PackageSize: 1, PackageCost: "1"},
		},
		Demands: []Demand{{Material: "b", Quantity: 3}},
		Rules: []Rule{
			{Name: "a2b", Input: Port{Material: "a", Quantity: 2}, Output: Port{Material: "b", Quantity: 3}},
			{Name: "b2a", Input: Port{Material: "b", Quantity: 3}, Output: Port{Material: "a", Quantity: 4}},
		},
	})
	var gc *GainCycleError
	if !errors.As(err, &gc) {
		t.Fatalf("err = %v, want GainCycleError", err)
	}
	if len(gc.Rules) != 2 {
		t.Fatalf("involved rules = %v, want both", gc.Rules)
	}
}

func TestLongGainCycleRejectedWithoutDepthCutoff(t *testing.T) {
	// A 6-hop cycle whose ratios multiply to 2. Depth-limited traversal
	// would silently miss or misreport this; the LP criterion is exact.
	names := []string{"m0", "m1", "m2", "m3", "m4", "m5"}
	mats := []Material{}
	for _, n := range names {
		mats = append(mats, Material{Name: n, PackageSize: 1, PackageCost: "1"})
	}
	rules := []Rule{}
	for i := 0; i < 5; i++ {
		rules = append(rules, Rule{
			Name:   fmt.Sprintf("step%d", i),
			Input:  Port{Material: names[i], Quantity: 1},
			Output: Port{Material: names[i+1], Quantity: 1},
		})
	}
	rules = append(rules, Rule{
		Name:   "close",
		Input:  Port{Material: names[5], Quantity: 1},
		Output: Port{Material: names[0], Quantity: 2},
	})
	err := solveErr(t, &Problem{
		Materials: mats,
		Demands:   []Demand{{Material: "m0", Quantity: 1}},
		Rules:     rules,
	})
	var gc *GainCycleError
	if !errors.As(err, &gc) {
		t.Fatalf("err = %v, want GainCycleError", err)
	}
	if len(gc.Rules) != 6 {
		t.Fatalf("involved rules = %v, want all 6", gc.Rules)
	}
}

func TestCompoundGainWithoutSimpleCycleRejected(t *testing.T) {
	// r1: 1 a -> 2 b; r2: 1 a -> 2 c; r3: 3 b + 3 c -> 4 a.
	// No single rule and no two-rule combination gains anything; the
	// witness is y = (3, 3, 2) with net (+2 a, 0, 0). Detection based on
	// traversing simple cycles (with or without a depth cutoff) cannot
	// find this; the exact LP criterion does.
	err := solveErr(t, &Problem{
		Materials: []Material{
			{Name: "a", PackageSize: 1, PackageCost: "1"},
			{Name: "b", PackageSize: 1, PackageCost: "1"},
			{Name: "c", PackageSize: 1, PackageCost: "1"},
		},
		Demands: []Demand{{Material: "a", Quantity: 1}},
		Rules: []Rule{
			{Name: "r1", Input: Port{Material: "a", Quantity: 1}, Output: Port{Material: "b", Quantity: 2}},
			{Name: "r2", Input: Port{Material: "a", Quantity: 1}, Output: Port{Material: "c", Quantity: 2}},
			{Name: "r3", Input: Port{Material: "b", Quantity: 3}, Output: Port{Material: "a", Quantity: 4},
				Aux: []Port{{Material: "c", Quantity: 3}}},
		},
	})
	var gc *GainCycleError
	if !errors.As(err, &gc) {
		t.Fatalf("err = %v, want GainCycleError", err)
	}
	if len(gc.Rules) != 3 {
		t.Fatalf("involved rules = %v, want all 3", gc.Rules)
	}
}

func TestBreakEvenCycleAllowed(t *testing.T) {
	// 1 a -> 1 b and 1 b -> 1 a conserve quantity: allowed, and the solver
	// still finds the cheap plan (buy a, convert).
	sol := mustSolve(t, &Problem{
		Materials: []Material{
			{Name: "a", PackageSize: 2, PackageCost: "3"},
			{Name: "b", PackageSize: 1, PackageCost: "10"},
		},
		Demands: []Demand{{Material: "b", Quantity: 2}},
		Rules: []Rule{
			{Name: "a2b", Input: Port{Material: "a", Quantity: 1}, Output: Port{Material: "b", Quantity: 1}},
			{Name: "b2a", Input: Port{Material: "b", Quantity: 1}, Output: Port{Material: "a", Quantity: 1}},
		},
	})
	if sol.TotalCost != "3" || applicationsOf(sol, "a2b") != 2 {
		t.Fatalf("got %+v", sol)
	}
}

func TestLossySelfLoopAllowed(t *testing.T) {
	// 2 a -> 1 a destroys quantity; harmless and must not be rejected.
	sol := mustSolve(t, &Problem{
		Materials: []Material{{Name: "a", PackageSize: 5, PackageCost: "4"}},
		Demands:   []Demand{{Material: "a", Quantity: 5}},
		Rules: []Rule{
			{Name: "waste", Input: Port{Material: "a", Quantity: 2}, Output: Port{Material: "a", Quantity: 1}},
		},
	})
	if sol.TotalCost != "4" || applicationsOf(sol, "waste") != 0 {
		t.Fatalf("got %+v", sol)
	}
}

func TestInfeasibleWithinLimits(t *testing.T) {
	err := solveErr(t, &Problem{
		Materials: []Material{{Name: "b", PackageSize: 1, PackageCost: "1"}},
		Demands:   []Demand{{Material: "b", Quantity: 100}},
		Limits:    Limits{MaxPackages: 5},
	})
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
}

func TestBranchAndBoundFindsIntegerOptimum(t *testing.T) {
	// LP relaxation is fractional (5 pB >= 9 -> pB = 1.8, cost 12.6);
	// integer optimum is pB = 2, cost 14.
	sol := mustSolve(t, &Problem{
		Materials: []Material{
			{Name: "a", PackageSize: 3, PackageCost: "5"},
			{Name: "b", PackageSize: 5, PackageCost: "7"},
			{Name: "c", PackageSize: 1, PackageCost: "100"},
		},
		Demands: []Demand{{Material: "c", Quantity: 9}},
		Rules: []Rule{
			{Name: "ra", Input: Port{Material: "a", Quantity: 1}, Output: Port{Material: "c", Quantity: 1}},
			{Name: "rb", Input: Port{Material: "b", Quantity: 1}, Output: Port{Material: "c", Quantity: 1}},
		},
	})
	if sol.TotalCost != "14" || packagesOf(sol, "b") != 2 {
		t.Fatalf("got %+v", sol)
	}
	if !sol.Proven {
		t.Fatalf("solution should be proven optimal")
	}
}

func TestNodeLimitCutoff(t *testing.T) {
	_, err := Solve(&Problem{
		Materials: []Material{
			{Name: "a", PackageSize: 3, PackageCost: "5"},
			{Name: "b", PackageSize: 5, PackageCost: "7"},
			{Name: "c", PackageSize: 1, PackageCost: "100"},
		},
		Demands: []Demand{{Material: "c", Quantity: 9}},
		Rules: []Rule{
			{Name: "ra", Input: Port{Material: "a", Quantity: 1}, Output: Port{Material: "c", Quantity: 1}},
			{Name: "rb", Input: Port{Material: "b", Quantity: 1}, Output: Port{Material: "c", Quantity: 1}},
		},
		Limits: Limits{MaxNodes: 1},
	})
	if !errors.Is(err, ErrNodeLimit) {
		t.Fatalf("err = %v, want ErrNodeLimit", err)
	}
}

func TestValidationErrors(t *testing.T) {
	bad := &Problem{
		Materials: []Material{{Name: "a", PackageSize: 1, PackageCost: "1"}},
		Demands:   []Demand{{Material: "ghost", Quantity: 1}},
	}
	if err := solveErr(t, bad); err == nil || !strings.Contains(err.Error(), "unknown material") {
		t.Fatalf("err = %v, want unknown material", err)
	}
	bad2 := &Problem{
		Materials: []Material{{Name: "a", PackageSize: 0, PackageCost: "1"}},
	}
	if err := solveErr(t, bad2); err == nil {
		t.Fatalf("want packageSize error")
	}
}

// TestAgainstBruteForce cross-checks the branch-and-bound optimum against
// exhaustive enumeration on small random instances (deterministic seed).
func TestAgainstBruteForce(t *testing.T) {
	rng := uint64(20261006)
	next := func(n int64) int64 { // xorshift, deterministic
		rng ^= rng << 13
		rng ^= rng >> 7
		rng ^= rng << 17
		return int64(rng % uint64(n))
	}

	for trial := 0; trial < 200; trial++ {
		nm := 2 + int(next(2)) // 2..3 materials
		nr := int(next(3))     // 0..2 rules
		p := &Problem{}
		for i := 0; i < nm; i++ {
			p.Materials = append(p.Materials, Material{
				Name:        fmt.Sprintf("m%d", i),
				PackageSize: 1 + next(4),
				PackageCost: fmt.Sprintf("%d", 1+next(9)),
			})
		}
		for r := 0; r < nr; r++ {
			in, out := int(next(int64(nm))), int(next(int64(nm)))
			rule := Rule{
				Name:   fmt.Sprintf("r%d", r),
				Input:  Port{Material: fmt.Sprintf("m%d", in), Quantity: 1 + next(3)},
				Output: Port{Material: fmt.Sprintf("m%d", out), Quantity: 1 + next(3)},
			}
			if next(2) == 0 { // sometimes consume an auxiliary material
				rule.Aux = []Port{{Material: fmt.Sprintf("m%d", int(next(int64(nm)))), Quantity: 1 + next(2)}}
			}
			p.Rules = append(p.Rules, rule)
		}
		p.Demands = []Demand{{Material: fmt.Sprintf("m%d", int(next(int64(nm)))), Quantity: 1 + next(8)}}
		p.Limits = Limits{MaxPackages: 3, MaxApplications: 4}

		if _, bad := DetectGainCycle(p); bad {
			continue // rejected instances are covered by dedicated tests
		}

		// Brute force over the same bounds.
		best := int64(-1)
		pkgs := make([]int64, nm)
		apps := make([]int64, nr)
		costs := make([]int64, nm)
		for i, m := range p.Materials {
			c, _ := new(big.Rat).SetString(m.PackageCost)
			costs[i] = c.Num().Int64()
		}
		var enum func(k int)
		enum = func(k int) {
			if k == nm {
				var enumApps func(r int)
				enumApps = func(r int) {
					if r == nr {
						cost := int64(0)
						for i := 0; i < nm; i++ {
							cost += pkgs[i] * costs[i]
						}
						if best >= 0 && cost >= best {
							return
						}
						for i := 0; i < nm; i++ {
							avail := pkgs[i] * p.Materials[i].PackageSize
							for r2 := 0; r2 < nr; r2++ {
								rule := p.Rules[r2]
								if rule.Output.Material == p.Materials[i].Name {
									avail += rule.Output.Quantity * apps[r2]
								}
								if rule.Input.Material == p.Materials[i].Name {
									avail -= rule.Input.Quantity * apps[r2]
								}
								for _, a := range rule.Aux {
									if a.Material == p.Materials[i].Name {
										avail -= a.Quantity * apps[r2]
									}
								}
							}
							if avail < p.demand[i] {
								return
							}
						}
						best = cost
						return
					}
					for apps[r] = 0; apps[r] <= p.Limits.MaxApplications; apps[r]++ {
						enumApps(r + 1)
					}
				}
				enumApps(0)
				return
			}
			for pkgs[k] = 0; pkgs[k] <= p.Limits.MaxPackages; pkgs[k]++ {
				enum(k + 1)
			}
		}
		enum(0)

		sol, err := Solve(p)
		if best < 0 {
			if !errors.Is(err, ErrInfeasible) {
				t.Fatalf("trial %d: brute force found no plan, solver err=%v sol=%+v", trial, err, sol)
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: brute force found cost %d but solver errored: %v", trial, best, err)
		}
		if sol.TotalCost != fmt.Sprintf("%d", best) {
			t.Fatalf("trial %d: solver cost %s, brute force %d (problem %+v)", trial, sol.TotalCost, best, p)
		}
	}
}
