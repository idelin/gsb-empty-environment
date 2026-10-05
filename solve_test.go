package main

import (
	"math/big"
	"math/rand"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat " + s)
	}
	return r
}

func rats(ss ...string) []*big.Rat {
	var out []*big.Rat
	for _, s := range ss {
		out = append(out, rat(s))
	}
	return out
}

func mkInstance(weights []string, positions []string, cap, lo, hi string) *Instance {
	in := &Instance{
		Capacity:  rat(cap),
		SafeLo:    rat(lo),
		SafeHi:    rat(hi),
		Positions: rats(positions...),
	}
	for i, w := range weights {
		in.Items = append(in.Items, Item{ID: string(rune('a' + i)), Weight: rat(w)})
	}
	if err := in.validate(); err != nil {
		panic(err)
	}
	return in
}

// TestExactFractions uses weights that have no exact binary float
// representation; the optimum must still be found exactly.
func TestExactFractions(t *testing.T) {
	in := mkInstance(
		[]string{"1/3", "1/3", "1/3"},
		[]string{"0", "1", "2"},
		"1", "0", "2",
	)
	sol := solve(in)
	if sol == nil {
		t.Fatal("no solution")
	}
	if len(sol.bins) != 1 {
		t.Fatalf("want 1 bin, got %d", len(sol.bins))
	}
	// cog of three equal weights at 0,1,2 is exactly 1.
	if sol.bins[0].cog.Cmp(rat("1")) != 0 {
		t.Fatalf("cog = %s, want 1", ratStr(sol.bins[0].cog))
	}
	if sol.maxEcc.Cmp(rat("0")) != 0 {
		t.Fatalf("maxEcc = %s, want 0", ratStr(sol.maxEcc))
	}
	if rep := verify(in, sol); !rep.OK {
		t.Fatalf("verify failed: %v", rep.Checks)
	}
}

// TestKnownOptimum: capacity forces >= 2 bins; balance forces the split.
func TestKnownOptimum(t *testing.T) {
	in := mkInstance(
		[]string{"6", "5", "4", "3"},
		[]string{"0", "1", "2", "3", "4"},
		"10", "1", "3",
	)
	sol := solve(in)
	if sol == nil {
		t.Fatal("no solution")
	}
	if len(sol.bins) != 2 {
		t.Fatalf("want 2 bins, got %d", len(sol.bins))
	}
	if rep := verify(in, sol); !rep.OK {
		t.Fatalf("verify failed: %v", rep.Checks)
	}
}

// TestInfeasible: one item heavier than capacity range can balance.
func TestInfeasible(t *testing.T) {
	in := mkInstance(
		[]string{"5"},
		[]string{"0", "1"},
		"10", "2", "3",
	)
	if sol := solve(in); sol != nil {
		t.Fatalf("expected infeasible, got %+v", sol)
	}
}

// bruteForce enumerates every assignment of items to (bin, slot) with at
// most maxBins bins and returns the lexicographic optimum (bins, maxEcc).
func bruteForce(t *testing.T, in *Instance, maxBins int) (int, *big.Rat) {
	n := len(in.Items)
	m := len(in.Positions)
	bestBins := maxBins + 1
	var bestEcc *big.Rat

	assign := make([][2]int, n) // item -> (bin, slot)
	var rec func(i int)
	rec = func(i int) {
		if i == n {
			used := 0
			for b := 0; b < maxBins; b++ {
				for _, a := range assign {
					if a[0] == b {
						used = b + 1
					}
				}
			}
			if used == 0 || used > bestBins {
				return
			}
			maxEcc := new(big.Rat)
			for b := 0; b < used; b++ {
				num := new(big.Rat)
				den := new(big.Rat)
				slots := map[int]bool{}
				cnt := 0
				for it := 0; it < n; it++ {
					if assign[it][0] != b {
						continue
					}
					cnt++
					if slots[assign[it][1]] {
						return
					}
					slots[assign[it][1]] = true
					w := in.Items[it].Weight
					num.Add(num, new(big.Rat).Mul(w, in.Positions[assign[it][1]]))
					den.Add(den, w)
				}
				if cnt == 0 || den.Cmp(in.Capacity) > 0 {
					return
				}
				cog := new(big.Rat).Quo(num, den)
				if cog.Cmp(in.SafeLo) < 0 || cog.Cmp(in.SafeHi) > 0 {
					return
				}
				ecc := new(big.Rat).Sub(cog, in.Target)
				ecc.Abs(ecc)
				if ecc.Cmp(maxEcc) > 0 {
					maxEcc = ecc
				}
			}
			if used < bestBins || (used == bestBins &&
				(bestEcc == nil || maxEcc.Cmp(bestEcc) < 0)) {
				bestBins = used
				bestEcc = maxEcc
			}
			return
		}
		for b := 0; b < maxBins; b++ {
			// symmetry: only open bin b if bin b-1 is in use
			if b > 0 {
				open := false
				for j := 0; j < i; j++ {
					if assign[j][0] == b-1 {
						open = true
					}
				}
				if !open {
					continue
				}
			}
			for p := 0; p < m; p++ {
				assign[i] = [2]int{b, p}
				rec(i + 1)
			}
		}
	}
	rec(0)
	return bestBins, bestEcc
}

// TestCrossCheck compares the solver against exhaustive enumeration on
// small random instances with awkward rational weights.
func TestCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 40; trial++ {
		n := 2 + rng.Intn(4) // 2..5 items
		m := 3 + rng.Intn(3) // 3..5 slots
		var weights []string
		dens := []string{"1", "2", "3", "4", "6"}
		for i := 0; i < n; i++ {
			weights = append(weights,
				string(rune('1'+rng.Intn(3)))+"/"+dens[rng.Intn(len(dens))])
		}
		var positions []string
		for p := 0; p < m; p++ {
			positions = append(positions, string(rune('0'+p)))
		}
		cap := string(rune('3' + rng.Intn(2)))
		in := mkInstance(weights, positions, cap, "1", "2")

		sol := solve(in)
		bb, be := bruteForce(t, in, n)
		if bb > n {
			if sol != nil {
				t.Fatalf("trial %d: solver found solution, brute force none", trial)
			}
			continue
		}
		if sol == nil {
			t.Fatalf("trial %d: brute force found %d bins, solver none", trial, bb)
		}
		if len(sol.bins) != bb {
			t.Fatalf("trial %d: bins %d != brute force %d", trial, len(sol.bins), bb)
		}
		if sol.maxEcc.Cmp(be) != 0 {
			t.Fatalf("trial %d: maxEcc %s != brute force %s",
				trial, ratStr(sol.maxEcc), ratStr(be))
		}
		if rep := verify(in, sol); !rep.OK {
			t.Fatalf("trial %d: verify failed: %v", trial, rep.Checks)
		}
	}
}

// TestVerifyTamper ensures the independent checker rejects tampered output.
func TestVerifyTamper(t *testing.T) {
	in := mkInstance(
		[]string{"1/3", "1/3", "1/3"},
		[]string{"0", "1", "2"},
		"1", "0", "2",
	)
	sol := solve(in)
	if sol == nil {
		t.Fatal("no solution")
	}
	sol.bins[0].cog = rat("3/2") // lie about the center of gravity
	if rep := verify(in, sol); rep.OK {
		t.Fatal("verify accepted a tampered center of gravity")
	}
}

// TestParseInstance checks JSON decoding of rationals in several forms.
func TestParseInstance(t *testing.T) {
	data := []byte(`{
	  "items": [{"id":"a","weight":"3/2"},{"id":"b","weight":1.5},{"id":"c","weight":2}],
	  "positions": ["0", "1/2", 1, "3/2"],
	  "capacity": "5",
	  "safe_interval": ["1/4", "3/4"]
	}`)
	in, err := parseInstance(data)
	if err != nil {
		t.Fatal(err)
	}
	if in.Items[0].Weight.Cmp(rat("3/2")) != 0 {
		t.Fatalf("weight a = %s", ratStr(in.Items[0].Weight))
	}
	if in.Items[1].Weight.Cmp(rat("3/2")) != 0 {
		t.Fatalf("weight b = %s", ratStr(in.Items[1].Weight))
	}
	if in.Target.Cmp(rat("1/2")) != 0 {
		t.Fatalf("default target = %s, want 1/2", ratStr(in.Target))
	}
}
