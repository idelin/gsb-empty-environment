package negotiate

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// bruteForce is the reference checker for small domains: it enumerates
// every candidate combination and keeps the best under the same objective,
// with no pruning and no budget.
func bruteForce(p *problem) (Selection, Score, bool) {
	n := len(p.feats)
	assign := make([]int, n)
	var bestSel Selection
	var bestScore Score
	have := false
	var rec func(i, w, v int)
	rec = func(i, w, v int) {
		if i == n {
			cand := make(Selection, n)
			for j, f := range p.feats {
				if assign[j] >= 0 {
					cand[f.name] = assign[j]
				}
			}
			if err := p.validate(cand); err != nil {
				return
			}
			sc := Score{Weight: w, Versions: v}
			if !have || p.better(sc, cand, bestScore, bestSel) {
				have, bestScore, bestSel = true, sc, cand
			}
			return
		}
		f := p.feats[i]
		for ver := f.hi; ver >= f.lo; ver-- {
			assign[i] = ver
			rec(i+1, w+f.priority, v+ver)
		}
		if !f.required {
			assign[i] = absent
			rec(i+1, w, v)
		}
		assign[i] = unassigned
	}
	rec(0, 0, 0)
	return bestSel, bestScore, have
}

// comboCount upper-bounds the brute-force search space so the reference
// check stays small-domain only.
func comboCount(p *problem) int64 {
	total := int64(1)
	for _, f := range p.feats {
		width := int64(f.hi - f.lo + 1)
		if !f.required {
			width++
		}
		total *= width
		if total > 200000 {
			return total
		}
	}
	return total
}

func crossCheck(t *testing.T, a, b *Profile) {
	t.Helper()
	prob, reasons := buildProblem(a, b)
	res := Negotiate(a, b, -1)
	if len(reasons) > 0 {
		if res.Status != StatusInfeasible {
			t.Fatalf("hard reasons but status %v", res.Status)
		}
		return
	}
	if comboCount(prob) > 200000 {
		t.Skip("domain too large for exhaustive reference")
	}
	refSel, refScore, ok := bruteForce(prob)
	switch {
	case !ok:
		if res.Status != StatusInfeasible {
			t.Fatalf("reference infeasible, solver says %v (%v)", res.Status, res.Selection)
		}
	case res.Status != StatusOK:
		t.Fatalf("reference feasible (%v), solver says %v", refSel, res.Status)
	default:
		if res.Score != refScore {
			t.Fatalf("score mismatch: solver %v, reference %v", res.Score, refScore)
		}
		if !reflect.DeepEqual(res.Selection, refSel) {
			t.Fatalf("selection mismatch: solver %v, reference %v", res.Selection, refSel)
		}
		if err := prob.validate(res.Selection); err != nil {
			t.Fatalf("solver selection invalid: %v", err)
		}
	}
}

func TestExhaustiveCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20260923))
	pool := []string{"alpha", "beta", "gamma", "delta"}
	for iter := 0; iter < 600; iter++ {
		mk := func(name string) *Profile {
			p := &Profile{Name: name}
			for _, n := range pool {
				if rng.Intn(100) < 60 { // 60% chance the side declares it
					d := FeatureDecl{
						Name:     n,
						Versions: VersionRange{Min: rng.Intn(3), Max: 0},
					}
					d.Versions.Max = d.Versions.Min + rng.Intn(3)
					d.Required = rng.Intn(100) < 15
					d.Priority = rng.Intn(9) - 2
					if rng.Intn(100) < 30 {
						req := Requirement{
							Feature: pool[rng.Intn(len(pool))],
							Range:   VersionRange{Min: rng.Intn(3), Max: 4},
						}
						if rng.Intn(100) < 40 {
							w := VersionRange{Min: rng.Intn(3), Max: 4}
							req.When = &w
						}
						d.Depends = append(d.Depends, req)
					}
					if rng.Intn(100) < 25 {
						d.Conflicts = append(d.Conflicts, pool[rng.Intn(len(pool))])
					}
					p.Features = append(p.Features, d)
				}
			}
			return p
		}
		a, b := mk("a"), mk("b")
		t.Run(fmt.Sprintf("iter-%d", iter), func(t *testing.T) {
			crossCheck(t, a, b)
		})
	}
}

func TestExhaustiveCrossCheckDeterministic(t *testing.T) {
	// A hand-built case exercising deps, conflicts and version conditions
	// together.
	when := VersionRange{2, 3}
	mk := func(name string, shift int) *Profile {
		return prof(name,
			FeatureDecl{Name: "tls", Versions: VersionRange{1, 3}, Required: true},
			FeatureDecl{Name: "comp", Versions: VersionRange{1, 2}, Priority: 5,
				Depends: []Requirement{{Feature: "tls", Range: VersionRange{2, 3}, When: &when}}},
			FeatureDecl{Name: "legacy", Versions: VersionRange{1, 1}, Priority: 1,
				Conflicts: []string{"comp"}},
			FeatureDecl{Name: "extra", Versions: VersionRange{0, 2 + shift}, Priority: 2},
		)
	}
	crossCheck(t, mk("a", 0), mk("b", 1))
}
