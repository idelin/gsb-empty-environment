package main

import (
	"math/rand"
	"testing"
)

// e builds a 0-based edge from 1-based object numbers.
func e(a, b int, w int64) edge { return edge{a - 1, b - 1, w} }

// E builds a raw 1-based edge literal for expected outputs.
func E(a, b int, w int64) edge { return edge{a, b, w} }

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalEdges(a, b []edge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSolve(t *testing.T) {
	tests := []struct {
		name     string
		n        int
		edges    []edge
		wantOpt  int64
		wantRank []int // 1-based
		wantViol []edge
	}{
		{
			name:     "no preferences",
			n:        3,
			edges:    nil,
			wantOpt:  0,
			wantRank: []int{1, 2, 3},
		},
		{
			name:     "consistent chain",
			n:        3,
			edges:    []edge{e(1, 2, 5), e(2, 3, 4), e(1, 3, 1)},
			wantOpt:  0,
			wantRank: []int{1, 2, 3},
		},
		{
			name:     "missing preferences imply nothing",
			n:        3,
			edges:    []edge{e(2, 1, 5)},
			wantOpt:  0,
			wantRank: []int{2, 1, 3},
		},
		{
			name:     "equal cycle is rankable",
			n:        3,
			edges:    []edge{e(1, 2, 1), e(2, 3, 1), e(3, 1, 1)},
			wantOpt:  1,
			wantRank: []int{1, 2, 3},
			wantViol: []edge{E(3, 1, 1)},
		},
		{
			name:     "weighted cycle breaks cheapest edge",
			n:        3,
			edges:    []edge{e(1, 2, 5), e(2, 3, 5), e(3, 1, 1)},
			wantOpt:  1,
			wantRank: []int{1, 2, 3},
			wantViol: []edge{E(3, 1, 1)},
		},
		{
			name:     "conflicting pair violates cheaper direction",
			n:        2,
			edges:    []edge{e(1, 2, 3), e(2, 1, 7)},
			wantOpt:  3,
			wantRank: []int{2, 1},
			wantViol: []edge{E(1, 2, 3)},
		},
		{
			name:     "parallel preferences aggregate",
			n:        2,
			edges:    []edge{e(1, 2, 2), e(1, 2, 3), e(2, 1, 4)},
			wantOpt:  4,
			wantRank: []int{1, 2},
			wantViol: []edge{E(2, 1, 4)},
		},
		{
			name:     "lexicographic tie break among optima",
			n:        4,
			edges:    []edge{e(4, 3, 1), e(3, 4, 1)},
			wantOpt:  1,
			wantRank: []int{1, 2, 3, 4},
			wantViol: []edge{E(4, 3, 1)},
		},
		{
			name:     "self preference ignored",
			n:        2,
			edges:    []edge{e(1, 1, 9), e(1, 2, 1)},
			wantOpt:  0,
			wantRank: []int{1, 2},
		},
		{
			name:     "zero objects",
			n:        0,
			edges:    nil,
			wantOpt:  0,
			wantRank: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opt, order, viol := solve(tc.n, tc.edges)
			if opt != tc.wantOpt {
				t.Errorf("opt = %d, want %d", opt, tc.wantOpt)
			}
			got := make([]int, len(order))
			for i, v := range order {
				got[i] = v + 1
			}
			if !equalInts(got, tc.wantRank) {
				t.Errorf("rank = %v, want %v", got, tc.wantRank)
			}
			var gotViol []edge
			for _, ed := range viol {
				gotViol = append(gotViol, edge{ed.from + 1, ed.to + 1, ed.w})
			}
			if !equalEdges(gotViol, tc.wantViol) {
				t.Errorf("violated = %v, want %v", gotViol, tc.wantViol)
			}
		})
	}
}

// bruteForce enumerates all permutations for cross-checking small cases.
func bruteForce(n int, edges []edge) (int64, []int) {
	w := make([][]int64, n)
	for i := range w {
		w[i] = make([]int64, n)
	}
	for _, ed := range edges {
		if ed.from != ed.to {
			w[ed.from][ed.to] += ed.w
		}
	}
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	best := int64(-1)
	var bestPerm []int
	var visit func(k int)
	visit = func(k int) {
		if k == n {
			var cost int64
			pos := make([]int, n)
			for i, v := range perm {
				pos[v] = i
			}
			for a := 0; a < n; a++ {
				for b := 0; b < n; b++ {
					if pos[a] > pos[b] {
						cost += w[a][b]
					}
				}
			}
			cp := append([]int(nil), perm...)
			if best < 0 || cost < best || (cost == best && less(cp, bestPerm)) {
				best = cost
				bestPerm = cp
			}
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			visit(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	visit(0)
	return best, bestPerm
}

func less(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func TestSolveMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := rng.Intn(8)
		var edges []edge
		if n > 0 {
			for i, m := 0, rng.Intn(12); i < m; i++ {
				edges = append(edges, edge{rng.Intn(n), rng.Intn(n), int64(rng.Intn(10))})
			}
		}
		opt, order, _ := solve(n, edges)
		wantOpt, wantOrder := bruteForce(n, edges)
		if opt != wantOpt {
			t.Fatalf("trial %d n=%d edges=%v: opt = %d, want %d", trial, n, edges, opt, wantOpt)
		}
		if !equalInts(order, wantOrder) {
			t.Fatalf("trial %d n=%d edges=%v: order = %v, want %v", trial, n, edges, order, wantOrder)
		}
	}
}

func TestSolveMaxSize(t *testing.T) {
	n := 18
	rng := rand.New(rand.NewSource(7))
	var edges []edge
	for i := 0; i < 120; i++ {
		edges = append(edges, edge{rng.Intn(n), rng.Intn(n), int64(1 + rng.Intn(1000))})
	}
	opt, order, viol := solve(n, edges)
	if len(order) != n {
		t.Fatalf("ranking length = %d, want %d", len(order), n)
	}
	var sum int64
	for _, ed := range viol {
		sum += ed.w
	}
	if sum != opt {
		t.Fatalf("violated weight sum %d != opt %d", sum, opt)
	}
}
