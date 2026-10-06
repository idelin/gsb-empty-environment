package knapbb

import (
	"bytes"
	"math/rand"
	"testing"
)

func randomInstance(r *rand.Rand, n int) Instance {
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{
			Value:  1 + r.Intn(20),
			Weight: 1 + r.Intn(15),
			Bound:  1 + r.Intn(3),
		}
	}
	total := 0
	for _, it := range items {
		total += it.Weight * it.Bound
	}
	return NewInstance(items, total/3+1)
}

// exhaustive is an independent brute-force optimum used to check the
// solver's optimality claims.
func exhaustive(inst Instance) int {
	best := 0
	counts := make([]int, len(inst.Items))
	var rec func(i, weight, value int)
	rec = func(i, weight, value int) {
		if weight > inst.Capacity {
			return
		}
		if i == len(inst.Items) {
			if value > best {
				best = value
			}
			return
		}
		it := inst.Items[i]
		for c := 0; c <= it.Bound; c++ {
			counts[i] = c
			rec(i+1, weight+c*it.Weight, value+c*it.Value)
		}
	}
	rec(0, 0, 0)
	return best
}

func traceOf(s *Solver) *[][]int {
	tr := &[][]int{}
	s.Trace = func(p []int) { *tr = append(*tr, p) }
	return tr
}

func equalTrace(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if comparePath(a[i], b[i]) != 0 || len(a[i]) != len(b[i]) {
			return false
		}
	}
	return true
}

// TestResumeMatchesUninterrupted runs each instance uninterrupted, then
// re-runs it with a tiny budget, checkpointing and reloading between
// slices. The popped-node traces, explored counts and incumbents must
// be identical: no node repeated, none skipped.
func TestResumeMatchesUninterrupted(t *testing.T) {
	for seed := int64(1); seed <= 30; seed++ {
		inst := randomInstance(rand.New(rand.NewSource(seed)), 7)

		full := NewSolver(inst)
		fullTrace := traceOf(full)
		fullRes := full.Run(1 << 30)
		if !fullRes.Done {
			t.Fatalf("seed %d: uninterrupted run did not finish", seed)
		}

		resumed := NewSolver(inst)
		resumedTrace := traceOf(resumed)
		var res Result
		sessions := 0
		for {
			res = resumed.Run(5)
			sessions++
			if res.Done {
				break
			}
			var buf bytes.Buffer
			if err := resumed.Save(&buf); err != nil {
				t.Fatalf("seed %d: save: %v", seed, err)
			}
			var err error
			resumed, err = Load(&buf)
			if err != nil {
				t.Fatalf("seed %d: load: %v", seed, err)
			}
			resumed.Trace = func(p []int) { *resumedTrace = append(*resumedTrace, p) }
			if sessions > 100000 {
				t.Fatalf("seed %d: too many resume sessions", seed)
			}
		}

		if !equalTrace(*fullTrace, *resumedTrace) {
			t.Fatalf("seed %d: trace mismatch (full=%d nodes, resumed=%d nodes)",
				seed, len(*fullTrace), len(*resumedTrace))
		}
		if res.Explored != fullRes.Explored {
			t.Fatalf("seed %d: explored %d != %d", seed, res.Explored, fullRes.Explored)
		}
		if res.BestValue != fullRes.BestValue {
			t.Fatalf("seed %d: best %d != %d", seed, res.BestValue, fullRes.BestValue)
		}
		if res.LowerBound != res.UpperBound {
			t.Fatalf("seed %d: done but bounds disagree: %d vs %d",
				seed, res.LowerBound, res.UpperBound)
		}
	}
}

// TestOptimalityVsExhaustive checks the solver's optimum against an
// independent exhaustive enumeration of all count vectors.
func TestOptimalityVsExhaustive(t *testing.T) {
	for seed := int64(100); seed < 140; seed++ {
		inst := randomInstance(rand.New(rand.NewSource(seed)), 8)
		res := NewSolver(inst).Run(1 << 30)
		if !res.Done {
			t.Fatalf("seed %d: not done", seed)
		}
		if want := exhaustive(inst); res.BestValue != want {
			t.Fatalf("seed %d: solver=%d exhaustive=%d", seed, res.BestValue, want)
		}
		// The returned counts must actually achieve the claimed value.
		w, v := 0, 0
		for i, c := range res.BestCounts {
			w += c * inst.Items[i].Weight
			v += c * inst.Items[i].Value
		}
		if w > inst.Capacity || v != res.BestValue {
			t.Fatalf("seed %d: counts infeasible or mismatched (w=%d v=%d best=%d)",
				seed, w, v, res.BestValue)
		}
	}
}

// TestBudgetExhaustionBounds checks that a budget-limited run brackets
// the true optimum and that a checkpoint taken at exhaustion resumes
// to the same optimum.
func TestBudgetExhaustionBounds(t *testing.T) {
	for seed := int64(200); seed < 230; seed++ {
		inst := randomInstance(rand.New(rand.NewSource(seed)), 8)
		opt := exhaustive(inst)

		s := NewSolver(inst)
		res := s.Run(3)
		if res.Done && res.BestValue != opt {
			t.Fatalf("seed %d: finished early with wrong value", seed)
		}
		if !(res.LowerBound <= opt && opt <= res.UpperBound) {
			t.Fatalf("seed %d: opt %d outside [%d, %d]",
				seed, opt, res.LowerBound, res.UpperBound)
		}

		var buf bytes.Buffer
		if err := s.Save(&buf); err != nil {
			t.Fatalf("seed %d: save: %v", seed, err)
		}
		s2, err := Load(&buf)
		if err != nil {
			t.Fatalf("seed %d: load: %v", seed, err)
		}
		final := s2.Run(1 << 30)
		if !final.Done || final.BestValue != opt {
			t.Fatalf("seed %d: resumed run got %d, want %d", seed, final.BestValue, opt)
		}
	}
}
