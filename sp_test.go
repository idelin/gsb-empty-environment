package main

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func build(t *testing.T, spec string) *Graph {
	t.Helper()
	g, err := ParseGraph(strings.NewReader(spec))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return g
}

func (g *Graph) id(name string) int {
	i, ok := g.Index[name]
	if !ok {
		return -1
	}
	return i
}

func checkPathEdges(t *testing.T, g *Graph, path []int, wantSrc, wantDst int) {
	t.Helper()
	if len(path) == 0 {
		t.Fatalf("empty path, want %d->%d", wantSrc, wantDst)
	}
	if path[0] != wantSrc || path[len(path)-1] != wantDst {
		t.Fatalf("path endpoints %d..%d, want %d..%d", path[0], path[len(path)-1], wantSrc, wantDst)
	}
	seen := map[int]bool{}
	for i, u := range path {
		if seen[u] {
			t.Fatalf("path visits node %d twice: %v", u, path)
		}
		seen[u] = true
		if i+1 < len(path) {
			if _, ok := g.MinEdgeWeight(u, path[i+1]); !ok {
				t.Fatalf("no edge %d->%d in path %v", u, path[i+1], path)
			}
		}
	}
}

func validateResult(t *testing.T, g *Graph, src, dst int, r TargetResult) {
	t.Helper()
	switch r.Status {
	case Finite:
		checkPathEdges(t, g, r.Path, src, dst)
		var sum int64
		for i := 0; i+1 < len(r.Path); i++ {
			w, _ := g.MinEdgeWeight(r.Path[i], r.Path[i+1])
			sum += w
		}
		if sum != r.Dist {
			t.Fatalf("path %v min-edge weight %d != dist %d", r.Path, sum, r.Dist)
		}
	case NegInf:
		if len(r.Cycle) == 0 {
			t.Fatalf("neginf without witness cycle")
		}
		seen := map[int]bool{}
		var cycMin int64
		for i, u := range r.Cycle {
			if seen[u] {
				t.Fatalf("cycle not simple: %v", r.Cycle)
			}
			seen[u] = true
			v := r.Cycle[(i+1)%len(r.Cycle)]
			w, ok := g.MinEdgeWeight(u, v)
			if !ok {
				t.Fatalf("cycle edge %d->%d missing: %v", u, v, r.Cycle)
			}
			cycMin += w
		}
		if r.CycleWeight >= 0 {
			t.Fatalf("witness cycle weight %d >= 0", r.CycleWeight)
		}
		if cycMin > r.CycleWeight {
			t.Fatalf("cycle min-edge weight %d > reported %d", cycMin, r.CycleWeight)
		}
		checkPathEdges(t, g, r.ToCycle, src, r.Cycle[0])
		checkPathEdges(t, g, r.FromCycle, r.Cycle[0], dst)
	}
}

func TestZeroCycle(t *testing.T) {
	g := build(t, "s a 1\na b -1\nb a 1\nb t 3\n")
	res := g.ShortestFrom(g.id("s"))
	for _, tc := range []struct {
		node string
		dist int64
	}{
		{"s", 0}, {"a", 1}, {"b", 0}, {"t", 3},
	} {
		r := res[g.id(tc.node)]
		if r.Status != Finite || r.Dist != tc.dist {
			t.Fatalf("%s: got %v/%d want finite/%d", tc.node, r.Status, r.Dist, tc.dist)
		}
		validateResult(t, g, g.id("s"), g.id(tc.node), r)
	}
}

func TestParallelEdges(t *testing.T) {
	g := build(t, "s t 5\ns t 3\ns a 1\na t 1\na t -2\n")
	res := g.ShortestFrom(g.id("s"))
	r := res[g.id("t")]
	if r.Status != Finite || r.Dist != -1 {
		t.Fatalf("t: got %v/%d want finite/-1", r.Status, r.Dist)
	}
	validateResult(t, g, g.id("s"), g.id("t"), r)
}

func TestParallelNegativeCycle(t *testing.T) {
	g := build(t, "s a 1\na b 2\na b -5\nb a 1\n")
	res := g.ShortestFrom(g.id("s"))
	for _, name := range []string{"a", "b"} {
		r := res[g.id(name)]
		if r.Status != NegInf {
			t.Fatalf("%s: got %v want -inf", name, r.Status)
		}
		validateResult(t, g, g.id("s"), g.id(name), r)
		if r.CycleWeight != -4 {
			t.Fatalf("cycle weight %d, want -4", r.CycleWeight)
		}
	}
}

func TestDisconnectedNegativeCycle(t *testing.T) {
	g := build(t, "s a 2\na t 3\nx y -2\ny x 1\n")
	res := g.ShortestFrom(g.id("s"))
	if r := res[g.id("t")]; r.Status != Finite || r.Dist != 5 {
		t.Fatalf("t: got %v/%d want finite/5", r.Status, r.Dist)
	}
	for _, name := range []string{"x", "y"} {
		if r := res[g.id(name)]; r.Status != Unreachable {
			t.Fatalf("%s: got %v want unreachable", name, r.Status)
		}
	}
	resX := g.ShortestFrom(g.id("x"))
	for _, name := range []string{"x", "y"} {
		r := resX[g.id(name)]
		if r.Status != NegInf {
			t.Fatalf("from x, %s: got %v want -inf", name, r.Status)
		}
		validateResult(t, g, g.id("x"), g.id(name), r)
	}
	for _, name := range []string{"s", "a", "t"} {
		if r := resX[g.id(name)]; r.Status != Unreachable {
			t.Fatalf("from x, %s: got %v want unreachable", name, r.Status)
		}
	}
}

func TestCycleReachableButTargetNot(t *testing.T) {
	g := build(t, "s c 1\nc d -3\nd c 1\ns t 5\nd u 1\n")
	res := g.ShortestFrom(g.id("s"))
	if r := res[g.id("t")]; r.Status != Finite || r.Dist != 5 {
		t.Fatalf("t: got %v/%d want finite/5 (cycle cannot reach t)", r.Status, r.Dist)
	}
	for _, name := range []string{"c", "d", "u"} {
		r := res[g.id(name)]
		if r.Status != NegInf {
			t.Fatalf("%s: got %v want -inf", name, r.Status)
		}
		validateResult(t, g, g.id("s"), g.id(name), r)
	}
}

func TestSelfLoop(t *testing.T) {
	g := build(t, "s a 1\na a -1\na b 2\nb b 0\n")
	res := g.ShortestFrom(g.id("s"))
	for _, name := range []string{"a", "b"} {
		r := res[g.id(name)]
		if r.Status != NegInf {
			t.Fatalf("%s: got %v want -inf", name, r.Status)
		}
		validateResult(t, g, g.id("s"), g.id(name), r)
	}
	if len(res[g.id("a")].Cycle) != 1 {
		t.Fatalf("self-loop witness cycle: %v", res[g.id("a")].Cycle)
	}
}

func TestSourceEqualsTarget(t *testing.T) {
	g := build(t, "s a 1\na s -3\ns b 1\n")
	res := g.ShortestFrom(g.id("s"))
	if r := res[g.id("s")]; r.Status != NegInf {
		t.Fatalf("s: got %v want -inf", r.Status)
	}
	validateResult(t, g, g.id("s"), g.id("s"), res[g.id("s")])
}

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 400; trial++ {
		n := 2 + rng.Intn(5)
		g := NewGraph()
		names := make([]string, n)
		for i := range names {
			names[i] = "n" + strconv.Itoa(i)
			g.Node(names[i])
		}
		for u := 0; u < n; u++ {
			for v := 0; v < n; v++ {
				for rng.Intn(100) < 30 {
					w := int64(rng.Intn(11) - 5)
					g.AddEdge(names[u], names[v], w)
				}
			}
		}
		for s := 0; s < n; s++ {
			res := g.ShortestFrom(s)
			for dst := 0; dst < n; dst++ {
				bs, bd := g.BruteStatus(s, dst)
				ds, dd := g.DPStatus(s, dst)
				got := res[dst]
				if got.Status != bs || bs != ds {
					t.Fatalf("trial %d %d->%d: bf=%v brute=%v dp=%v", trial, s, dst, got.Status, bs, ds)
				}
				if bs == Finite && (got.Dist != bd || bd != dd) {
					t.Fatalf("trial %d %d->%d: dist bf=%d brute=%d dp=%d", trial, s, dst, got.Dist, bd, dd)
				}
				validateResult(t, g, s, dst, got)
			}
		}
	}
}
