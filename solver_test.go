package main

import (
	"math/rand"
	"testing"
)

// TestAgainstBruteForce cross-checks the exact DP against exhaustive
// enumeration on many small random histograms, including empty levels.
func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 2 + rng.Intn(8) // 2..9 levels
		w := make([]int64, n)
		for i := range w {
			w[i] = int64(rng.Intn(6)) // zeros allowed
		}
		m := 0
		for _, c := range w {
			if c > 0 {
				m++
			}
		}
		if m == 0 {
			continue
		}
		for k := 1; k <= m+1 && k <= 4; k++ {
			sol := Solve(w, k)
			be, bs := BruteForce(w, k)
			if sol.MaxErr != be || sol.SSE != bs {
				t.Fatalf("trial %d n=%d k=%d w=%v: dp=(%d,%d) brute=(%d,%d)",
					trial, n, k, w, sol.MaxErr, sol.SSE, be, bs)
			}
		}
	}
}

// TestLexicographicNotMSE is a case where the MSE-optimal partition has a
// strictly larger max error; the solver must prefer maxerr=1, sse=202 over
// the MSE-optimal maxerr=2, sse=8 (partition [0..2][3..5]).
func TestLexicographicNotMSE(t *testing.T) {
	w := []int64{1, 0, 100, 100, 0, 1}
	sol := Solve(w, 2)
	if sol.MaxErr != 1 || sol.SSE != 202 {
		t.Fatalf("got (maxerr=%d, sse=%d), want (1, 202)", sol.MaxErr, sol.SSE)
	}
	// Sanity: an MSE-first solver would land here instead.
	if BruteForceMSEOnly(w, 2) != 8 {
		t.Fatal("test invariant broken: expected MSE-only optimum of 8")
	}
}

// BruteForceMSEOnly returns the minimal achievable SSE ignoring max error,
// to demonstrate that it differs from the lexicographic objective.
func BruteForceMSEOnly(w []int64, k int) int64 {
	n := len(w)
	best := int64(1<<62 - 1)
	var rec func(start, left int, acc int64)
	rec = func(start, left int, acc int64) {
		if left == 1 {
			// whole tail as one segment, best integer rep
			var cnt, s1, s2 int64
			for i := start; i < n; i++ {
				cnt += w[i]
				s1 += w[i] * int64(i)
				s2 += w[i] * int64(i) * int64(i)
			}
			if cnt == 0 {
				if acc < best {
					best = acc
				}
				return
			}
			sse := func(r int) int64 {
				return s2 - 2*int64(r)*s1 + int64(r)*int64(r)*cnt
			}
			q := int(s1 / cnt)
			c := sse(q)
			if c2 := sse(q + 1); c2 < c {
				c = c2
			}
			if acc+c < best {
				best = acc + c
			}
			return
		}
		for end := start + 1; end <= n-left+1; end++ {
			var cnt, s1, s2 int64
			for i := start; i < end; i++ {
				cnt += w[i]
				s1 += w[i] * int64(i)
				s2 += w[i] * int64(i) * int64(i)
			}
			if cnt == 0 {
				continue
			}
			sse := func(r int) int64 {
				return s2 - 2*int64(r)*s1 + int64(r)*int64(r)*cnt
			}
			q := int(s1 / cnt)
			c := sse(q)
			if c2 := sse(q + 1); c2 < c {
				c = c2
			}
			rec(end, left-1, acc+c)
		}
	}
	rec(0, k, 0)
	return best
}

// TestStructure checks group contiguity, coverage, and integer reps.
func TestStructure(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 100; trial++ {
		n := 1 + rng.Intn(30)
		w := make([]int64, n)
		m := 0
		for i := range w {
			w[i] = int64(rng.Intn(100))
			if w[i] > 0 {
				m++
			}
		}
		if m == 0 {
			continue
		}
		k := 1 + rng.Intn(m)
		sol := Solve(w, k)
		if len(sol.Groups) != k && k <= m {
			t.Fatalf("k=%d but got %d groups", k, len(sol.Groups))
		}
		pos := 0
		var total int64
		maxErr := 0
		for _, g := range sol.Groups {
			if g.From != pos {
				t.Fatalf("groups not contiguous/covering: %+v", sol.Groups)
			}
			pos = g.To + 1
			if g.Rep < 0 || g.Rep > n-1 {
				t.Fatalf("rep %d out of range", g.Rep)
			}
			// Recompute group stats independently.
			var cnt, sse int64
			me := 0
			for i := g.From; i <= g.To; i++ {
				if w[i] == 0 {
					continue
				}
				d := iabs(i - g.Rep)
				if d > me {
					me = d
				}
				cnt += w[i]
				sse += w[i] * int64(d) * int64(d)
			}
			if cnt != g.Pixels || sse != g.SSE || me != g.MaxErr {
				t.Fatalf("group stats wrong: %+v recomputed (%d,%d,%d)",
					g, cnt, sse, me)
			}
			total += sse
			if me > maxErr {
				maxErr = me
			}
		}
		if pos != n {
			t.Fatalf("groups do not cover all levels: %+v", sol.Groups)
		}
		if total != sol.SSE || maxErr != sol.MaxErr {
			t.Fatalf("totals mismatch: (%d,%d) vs (%d,%d)",
				sol.MaxErr, sol.SSE, maxErr, total)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	// Empty histogram.
	if sol := Solve(make([]int64, 256), 4); len(sol.Groups) != 0 {
		t.Fatal("empty histogram should yield no groups")
	}
	// k larger than the number of non-empty levels: error 0.
	w := make([]int64, 256)
	w[10], w[200] = 5, 7
	sol := Solve(w, 100)
	if sol.MaxErr != 0 || sol.SSE != 0 || len(sol.Groups) != 2 {
		t.Fatalf("clamped k: got %+v", sol)
	}
	// k = 1.
	w2 := []int64{3, 0, 0, 3}
	sol = Solve(w2, 1)
	if sol.MaxErr != 2 || len(sol.Groups) != 1 || sol.Groups[0].Rep != 2 && sol.Groups[0].Rep != 1 {
		t.Fatalf("k=1: got %+v", sol)
	}
	// Empty levels absorbed: no group may be forced onto them.
	w3 := []int64{4, 0, 0, 0, 4}
	sol = Solve(w3, 2)
	if sol.MaxErr != 0 || sol.SSE != 0 {
		t.Fatalf("empty levels should be absorbable: got %+v", sol)
	}
}
