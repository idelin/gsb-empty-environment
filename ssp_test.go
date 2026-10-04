package ssp

import (
	"math"
	"math/rand"
	"testing"
)

func buildGraph(edges [][3]interface{}) *Graph {
	g := NewGraph()
	for _, e := range edges {
		g.AddEdge(e[0].(string), e[1].(string), e[2].(float64))
	}
	return g
}

func mustStatus(t *testing.T, g *Graph, res *Result, name string, want Status) {
	t.Helper()
	id, ok := g.Lookup(name)
	if !ok {
		t.Fatalf("unknown vertex %s", name)
	}
	if res.Status[id] != want {
		t.Errorf("vertex %s: got status %s, want %s", name, res.Status[id], want)
	}
}

func mustDist(t *testing.T, g *Graph, res *Result, name string, want float64) {
	t.Helper()
	id, _ := g.Lookup(name)
	if res.Status[id] != Finite {
		t.Fatalf("vertex %s: status %s, want finite %g", name, res.Status[id], want)
	}
	if res.Dist[id] != want {
		t.Errorf("vertex %s: dist %g, want %g", name, res.Dist[id], want)
	}
}

func TestFiniteDistancesAndPaths(t *testing.T) {
	g := buildGraph([][3]interface{}{
		{"s", "a", 2.0},
		{"s", "b", 5.0},
		{"a", "b", 1.0},
		{"b", "c", -2.0}, // negative edge, no negative cycle
		{"a", "c", 10.0},
	})
	s, _ := g.Lookup("s")
	res := ShortestPaths(g, s)
	mustDist(t, g, res, "s", 0)
	mustDist(t, g, res, "a", 2)
	mustDist(t, g, res, "b", 3)
	mustDist(t, g, res, "c", 1)
	c, _ := g.Lookup("c")
	path := res.Path(g, c)
	want := []string{"s", "a", "b", "c"}
	if len(path) != len(want) {
		t.Fatalf("path length %d, want %d", len(path), len(want))
	}
	for i, v := range path {
		if g.Names[v] != want[i] {
			t.Errorf("path[%d] = %s, want %s", i, g.Names[v], want[i])
		}
	}
}

func TestUnreachable(t *testing.T) {
	g := buildGraph([][3]interface{}{
		{"s", "a", 1.0},
		{"x", "y", 1.0},
	})
	s, _ := g.Lookup("s")
	res := ShortestPaths(g, s)
	mustStatus(t, g, res, "x", Unreachable)
	mustStatus(t, g, res, "y", Unreachable)
	mustStatus(t, g, res, "a", Finite)
}

// A negative cycle only pollutes vertices it can actually reach; the source
// and upstream vertices keep finite distances.
func TestNegativeCycleScope(t *testing.T) {
	g := buildGraph([][3]interface{}{
		{"s", "a", 1.0},
		{"a", "b", 1.0},
		{"b", "c", -3.0},
		{"c", "b", 1.0}, // cycle b->c->b weight -2
		{"c", "t", 1.0},
		{"s", "u", 4.0}, // u reachable, not via the cycle
	})
	s, _ := g.Lookup("s")
	res := ShortestPaths(g, s)
	mustStatus(t, g, res, "s", Finite)
	mustStatus(t, g, res, "a", Finite)
	mustStatus(t, g, res, "u", Finite)
	mustStatus(t, g, res, "b", NegInf)
	mustStatus(t, g, res, "c", NegInf)
	mustStatus(t, g, res, "t", NegInf)
	mustDist(t, g, res, "a", 1)
	mustDist(t, g, res, "u", 4)
}

// Zero-weight cycles must not be treated as negative cycles.
func TestZeroCycle(t *testing.T) {
	g := buildGraph([][3]interface{}{
		{"s", "a", 1.0},
		{"a", "b", 2.0},
		{"b", "a", -2.0}, // cycle a->b->a weight 0
		{"b", "t", 3.0},
		{"z", "z", 0.0}, // zero self-loop, disconnected
	})
	s, _ := g.Lookup("s")
	res := ShortestPaths(g, s)
	for _, v := range []string{"s", "a", "b", "t"} {
		mustStatus(t, g, res, v, Finite)
	}
	mustDist(t, g, res, "a", 1)
	mustDist(t, g, res, "b", 3)
	mustDist(t, g, res, "t", 6)
	mustStatus(t, g, res, "z", Unreachable)
}

// Parallel edges: the cheapest one wins; a negative 2-cycle formed by
// anti-parallel edges is detected.
func TestParallelEdges(t *testing.T) {
	g := buildGraph([][3]interface{}{
		{"s", "a", 5.0},
		{"s", "a", 2.0}, // parallel, cheaper
		{"s", "a", 9.0}, // parallel, dearer
		{"a", "t", 1.0},
		{"p", "q", -4.0},
		{"p", "q", 1.0}, // parallel alternative
		{"q", "p", 2.0}, // 2-cycle p->q->p weight -2 via the -4 edge
		{"s", "p", 1.0},
	})
	s, _ := g.Lookup("s")
	res := ShortestPaths(g, s)
	mustDist(t, g, res, "a", 2)
	mustDist(t, g, res, "t", 3)
	mustStatus(t, g, res, "p", NegInf)
	mustStatus(t, g, res, "q", NegInf)
}

// A negative cycle in a disconnected component must not pollute anything;
// likewise a cycle reachable from the source but unable to reach the target
// leaves that target finite.
func TestDisconnectedAndOneSidedNegativeCycles(t *testing.T) {
	g := buildGraph([][3]interface{}{
		{"s", "a", 1.0},
		{"a", "t", 1.0},
		{"x", "y", -5.0},
		{"y", "x", 1.0}, // negative cycle, disconnected from s
		{"s", "c", 1.0},
		{"c", "d", -2.0},
		{"d", "c", 1.0},  // negative cycle reachable from s...
		{"s", "t", 10.0}, // ...but it cannot reach t
	})
	s, _ := g.Lookup("s")
	res := ShortestPaths(g, s)
	mustStatus(t, g, res, "x", Unreachable)
	mustStatus(t, g, res, "y", Unreachable)
	mustStatus(t, g, res, "c", NegInf)
	mustStatus(t, g, res, "d", NegInf)
	mustStatus(t, g, res, "t", Finite)
	mustDist(t, g, res, "t", 2)
}

func TestNegativeSelfLoop(t *testing.T) {
	g := buildGraph([][3]interface{}{
		{"s", "a", 1.0},
		{"a", "a", -0.5},
		{"a", "t", 1.0},
		{"s", "ok", 2.0},
	})
	s, _ := g.Lookup("s")
	res := ShortestPaths(g, s)
	mustStatus(t, g, res, "a", NegInf)
	mustStatus(t, g, res, "t", NegInf)
	mustStatus(t, g, res, "ok", Finite)
}

// Multiple sources over the same graph give independent answers.
func TestMultipleSources(t *testing.T) {
	g := buildGraph([][3]interface{}{
		{"s1", "m", 1.0},
		{"m", "n", -3.0},
		{"n", "m", 1.0}, // negative cycle m<->n
		{"n", "t", 1.0},
		{"s2", "t", 7.0},
	})
	s1, _ := g.Lookup("s1")
	s2, _ := g.Lookup("s2")
	r1 := ShortestPaths(g, s1)
	r2 := ShortestPaths(g, s2)
	mustStatus(t, g, r1, "t", NegInf)
	mustStatus(t, g, r2, "t", Finite)
	mustDist(t, g, r2, "t", 7)
	mustStatus(t, g, r2, "m", Unreachable)
}

// Evidence must be a simple negative cycle, reachable from the source, able
// to reach the target, with valid connecting paths.
func checkEvidence(t *testing.T, g *Graph, res *Result, target string) {
	t.Helper()
	tid, _ := g.Lookup(target)
	if res.Status[tid] != NegInf {
		t.Fatalf("%s: status %s, want NegInf", target, res.Status[tid])
	}
	ev := res.Evidence(g, tid)
	if ev == nil {
		t.Fatalf("%s: no evidence produced", target)
	}
	// Simple: no repeated vertices.
	seen := make(map[int]bool)
	for _, v := range ev.Cycle {
		if seen[v] {
			t.Fatalf("%s: cycle not simple: %v", target, ev.Cycle)
		}
		seen[v] = true
	}
	// Negative, and hops exist with matching edges.
	if ev.CycleWeight >= 0 {
		t.Fatalf("%s: cycle weight %g not negative", target, ev.CycleWeight)
	}
	total := 0.0
	for i, u := range ev.Cycle {
		v := ev.Cycle[(i+1)%len(ev.Cycle)]
		e := g.Edges[ev.CycleEdges[i]]
		if e.From != u || e.To != v {
			t.Fatalf("%s: cycle edge %d mismatch", target, i)
		}
		total += e.Weight
	}
	if total != ev.CycleWeight {
		t.Fatalf("%s: cycle weight mismatch %g vs %g", target, total, ev.CycleWeight)
	}
	checkPath := func(p []int, from, to int, what string) {
		if len(p) == 0 || p[0] != from || p[len(p)-1] != to {
			t.Fatalf("%s: bad %s path %v", target, what, p)
		}
		for i := 0; i+1 < len(p); i++ {
			if _, ok := g.MinEdgeWeight(p[i], p[i+1]); !ok {
				t.Fatalf("%s: %s path uses missing edge %s->%s",
					target, what, g.Names[p[i]], g.Names[p[i+1]])
			}
		}
	}
	checkPath(ev.FromSource, res.Source, ev.Cycle[0], "source->cycle")
	checkPath(ev.ToTarget, ev.Cycle[0], tid, "cycle->target")
}

func TestEvidence(t *testing.T) {
	g := buildGraph([][3]interface{}{
		{"s", "a", 1.0},
		{"a", "b", 1.0},
		{"b", "c", -3.0},
		{"c", "b", 1.0},
		{"c", "t", 1.0},
		{"s", "self", 1.0},
		{"self", "self", -1.0},
		{"self", "t2", 1.0},
	})
	s, _ := g.Lookup("s")
	res := ShortestPaths(g, s)
	checkEvidence(t, g, res, "t")
	checkEvidence(t, g, res, "b")
	checkEvidence(t, g, res, "t2")
	checkEvidence(t, g, res, "self")
	// No evidence for finite/unreachable targets.
	a, _ := g.Lookup("a")
	if res.Evidence(g, a) != nil {
		t.Errorf("expected nil evidence for finite vertex a")
	}
}

// Randomised cross-check: Bellman-Ford statuses must agree with brute-force
// simple-cycle enumeration, and finite distances must agree with an
// independent finite-step DP. The generator emits parallel edges,
// self-loops, zero cycles and disconnected components.
func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(7)
		g := NewGraph()
		names := make([]string, n)
		for i := range names {
			names[i] = string(rune('a' + i))
			g.Vertex(names[i])
		}
		m := rng.Intn(2 * n * n)
		for i := 0; i < m; i++ {
			u := names[rng.Intn(n)]
			v := names[rng.Intn(n)]
			w := float64(rng.Intn(11) - 5) // weights in [-5, 5]
			g.AddEdge(u, v, w)
		}
		for src := 0; src < n; src++ {
			res := ShortestPaths(g, src)
			for _, p := range CrossCheck(g, src, res) {
				t.Errorf("trial %d: %s", trial, p)
			}
			dp := FiniteStepDP(g, src)
			for v := 0; v < n; v++ {
				if res.Status[v] != Finite {
					continue
				}
				if math.Abs(dp[v]-res.Dist[v]) > 1e-9 {
					t.Errorf("trial %d src %d v %d: bf=%g dp=%g",
						trial, src, v, res.Dist[v], dp[v])
				}
			}
			// Evidence spot-check on affected targets.
			for v := 0; v < n; v++ {
				if res.Status[v] == NegInf {
					ev := res.Evidence(g, v)
					if ev == nil {
						t.Errorf("trial %d src %d: no evidence for NegInf vertex %d", trial, src, v)
						continue
					}
					if ev.CycleWeight >= 0 {
						t.Errorf("trial %d src %d: evidence cycle weight %g", trial, src, ev.CycleWeight)
					}
				}
			}
			if t.Failed() {
				t.Fatalf("aborting on trial %d", trial)
			}
		}
	}
}

// Zero cycles among random graphs: with edge weights derived from vertex
// potentials (w(u,v) = pot[v]-pot[u]) every cycle telescopes to exactly
// zero, so no vertex may ever be reported NegInf.
func TestRandomZeroCycleSafety(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 100; trial++ {
		n := 2 + rng.Intn(6)
		g := NewGraph()
		names := make([]string, n)
		pot := make([]float64, n)
		for i := range names {
			names[i] = string(rune('a' + i))
			g.Vertex(names[i])
			pot[i] = float64(rng.Intn(9) - 4)
		}
		m := rng.Intn(2 * n * n)
		for i := 0; i < m; i++ {
			u := rng.Intn(n)
			v := rng.Intn(n)
			g.AddEdge(names[u], names[v], pot[v]-pot[u]) // cycles sum to 0
		}
		res := ShortestPaths(g, 0)
		for v := 0; v < n; v++ {
			if res.Status[v] == NegInf {
				t.Fatalf("trial %d: vertex %d wrongly NegInf with only zero cycles", trial, v)
			}
		}
		for _, p := range CrossCheck(g, 0, res) {
			t.Errorf("trial %d: %s", trial, p)
		}
	}
}
