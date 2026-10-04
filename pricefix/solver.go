package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
)

// inf is used as "infinite" capacity / unreachable distance. All real costs
// are assumed to be far below this magnitude (|prices|, |bounds| <= 1e15).
const inf = int64(1) << 60

// Constraint represents Lo <= p[V] - p[U] <= Hi. Either side may be missing.
type Constraint struct {
	U, V         int
	Lo, Hi       int64
	HasLo, HasHi bool
}

// Problem is one price-fixing instance.
type Problem struct {
	N      int
	A      []int64 // original prices
	Locked []bool  // Locked[i]: price of item i may not change
	Cons   []Constraint
}

// Edge kinds in the difference-constraint graph. Node p.N is the constant-0
// ground node G; fixing p_i = a_i is the edge pair G->i (w=a_i), i->G (w=-a_i).
const (
	edgeConHi = iota // p[V] - p[U] <= Hi
	edgeConLo        // p[U] - p[V] <= -Lo
	edgeFixHi        // p[i] - 0 <= A[i]
	edgeFixLo        // 0 - p[i] <= -A[i]
)

type fEdge struct {
	from, to int
	w        int64
	kind     int
	idx      int // constraint index or item index
}

// Solution is the result of Solve.
type Solution struct {
	Feasible  bool
	Prices    []int64
	Changes   int     // number of items whose price differs from the original
	Deviation int64   // total absolute adjustment
	Changed   []int   // indices of changed items (0-based)
	Cycle     []fEdge // infeasibility certificate: negative cycle
	CycleSum  int64
}

func abs64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// buildEdges builds the difference-constraint graph for the given kept set
// (items forced to their original price). Node p.N is the ground node G.
func buildEdges(p Problem, keptMask uint64) []fEdge {
	edges := make([]fEdge, 0, 2*len(p.Cons)+2*p.N)
	for k, c := range p.Cons {
		if c.HasHi {
			edges = append(edges, fEdge{from: c.U, to: c.V, w: c.Hi, kind: edgeConHi, idx: k})
		}
		if c.HasLo {
			edges = append(edges, fEdge{from: c.V, to: c.U, w: -c.Lo, kind: edgeConLo, idx: k})
		}
	}
	for i := 0; i < p.N; i++ {
		if keptMask>>i&1 == 1 {
			edges = append(edges, fEdge{from: p.N, to: i, w: p.A[i], kind: edgeFixHi, idx: i})
			edges = append(edges, fEdge{from: i, to: p.N, w: -p.A[i], kind: edgeFixLo, idx: i})
		}
	}
	return edges
}

// checkFeasible reports whether the constraints are satisfiable with the kept
// items fixed at their original prices. If not, it returns a negative cycle
// (edges in forward order) as an infeasibility certificate.
func checkFeasible(p Problem, keptMask uint64) (bool, []fEdge) {
	edges := buildEdges(p, keptMask)
	nv := p.N + 1
	dist := make([]int64, nv) // virtual super-source: all distances start at 0
	pred := make([]int, nv)
	for i := range pred {
		pred[i] = -1
	}
	updated := -1
	for iter := 1; iter <= nv; iter++ {
		updated = -1
		for ei, e := range edges {
			if dist[e.from]+e.w < dist[e.to] {
				dist[e.to] = dist[e.from] + e.w
				pred[e.to] = ei
				updated = e.to
			}
		}
		if updated == -1 {
			return true, nil
		}
	}
	// A node still relaxed in the nv-th round lies on/reaches a negative cycle.
	x := updated
	for i := 0; i < nv; i++ {
		x = edges[pred[x]].from
	}
	start := x
	var cyc []fEdge
	for {
		ei := pred[x]
		cyc = append(cyc, edges[ei])
		x = edges[ei].from
		if x == start {
			break
		}
	}
	for i, j := 0, len(cyc)-1; i < j; i, j = i+1, j-1 {
		cyc[i], cyc[j] = cyc[j], cyc[i]
	}
	return false, cyc
}

// ---- min-cost circulation (for the secondary objective) ----

type mfEdge struct {
	to, rev   int
	cap, cost int64
}

type mcf struct{ g [][]mfEdge }

func newMcf(n int) *mcf { return &mcf{g: make([][]mfEdge, n)} }

func (m *mcf) add(u, v int, cap, cost int64) {
	m.g[u] = append(m.g[u], mfEdge{to: v, rev: len(m.g[v]), cap: cap, cost: cost})
	m.g[v] = append(m.g[v], mfEdge{to: u, rev: len(m.g[u]) - 1, cap: 0, cost: -cost})
}

// optimize solves, for a fixed kept set, the LP
//
//	min  sum_{i free} |p_i - a_i|
//	s.t. all difference constraints, kept items pinned to a_i.
//
// The LP is the dual of a min-cost circulation: each free item may absorb or
// inject one unit of flow (edges S->i cost a_i, i->T cost -a_i, capacity 1),
// constraints are infinite-capacity arcs priced by their bounds, and the
// ground node G (pinned to 0 via S->G, G->T) anchors the fixed items.
// Optimal node potentials (shortest paths from {S,T} in the residual graph)
// are optimal prices. All data is integral, so the optimum is integral.
func optimize(p Problem, keptMask uint64) (int64, []int64) {
	n := p.N
	G, S, T := n, n+1, n+2
	nn := n + 3
	m := newMcf(nn)
	for _, c := range p.Cons {
		if c.HasHi {
			m.add(c.U, c.V, inf, c.Hi)
		}
		if c.HasLo {
			m.add(c.V, c.U, inf, -c.Lo)
		}
	}
	for i := 0; i < n; i++ {
		if keptMask>>i&1 == 1 {
			m.add(G, i, inf, p.A[i])
			m.add(i, G, inf, -p.A[i])
		} else {
			m.add(S, i, 1, p.A[i])
			m.add(i, T, 1, -p.A[i])
		}
	}
	m.add(S, G, inf, 0)
	m.add(G, T, inf, 0)

	// Successive shortest augmenting paths: augment while a negative-cost
	// S->T path exists. Feasibility was checked beforehand, so the initial
	// residual graph has no negative cycles and each augmentation (<= 2n)
	// saturates at least one unit-capacity edge.
	for {
		dist := make([]int64, nn)
		for i := range dist {
			dist[i] = inf
		}
		dist[S] = 0
		pe := make([]int, nn)
		pv := make([]int, nn)
		for iter := 0; iter < nn; iter++ {
			upd := false
			for u := 0; u < nn; u++ {
				if dist[u] == inf {
					continue
				}
				for ei, e := range m.g[u] {
					if e.cap > 0 && dist[u]+e.cost < dist[e.to] {
						dist[e.to] = dist[u] + e.cost
						pv[e.to] = u
						pe[e.to] = ei
						upd = true
					}
				}
			}
			if !upd {
				break
			}
		}
		if dist[T] == inf || dist[T] >= 0 {
			break
		}
		d := inf
		for v := T; v != S; v = pv[v] {
			if c := m.g[pv[v]][pe[v]].cap; c < d {
				d = c
			}
		}
		for v := T; v != S; v = pv[v] {
			e := &m.g[pv[v]][pe[v]]
			e.cap -= d
			m.g[v][e.rev].cap += d
		}
	}

	// Optimal potentials: shortest paths in the residual graph with
	// pi_S = pi_T = 0 pinned (multi-source Bellman-Ford).
	d := make([]int64, nn)
	for i := range d {
		d[i] = inf
	}
	d[S], d[T] = 0, 0
	for iter := 0; iter < nn; iter++ {
		upd := false
		for u := 0; u < nn; u++ {
			if d[u] == inf {
				continue
			}
			for _, e := range m.g[u] {
				if e.cap > 0 && d[u]+e.cost < d[e.to] {
					d[e.to] = d[u] + e.cost
					upd = true
				}
			}
		}
		if !upd {
			break
		}
	}

	prices := make([]int64, n)
	var dev int64
	for i := 0; i < n; i++ {
		if keptMask>>i&1 == 1 {
			prices[i] = p.A[i]
		} else {
			prices[i] = d[i] - d[G]
			dev += abs64(prices[i] - p.A[i])
		}
	}
	return dev, prices
}

// combinations returns all k-bit masks over u bit positions.
func combinations(u, k int) []uint64 {
	var out []uint64
	var rec func(pos, left int, mask uint64)
	rec = func(pos, left int, mask uint64) {
		if left == 0 {
			out = append(out, mask)
			return
		}
		if pos >= u {
			return
		}
		rec(pos+1, left-1, mask|1<<uint(pos))
		if u-pos-1 >= left {
			rec(pos+1, left, mask)
		}
	}
	rec(0, k, 0)
	return out
}

func expandMask(mask uint64, unlocked []int, lockedMask uint64) uint64 {
	kept := lockedMask
	for pos, item := range unlocked {
		if mask>>uint(pos)&1 == 1 {
			kept |= 1 << uint(item)
		}
	}
	return kept
}

func parallelFor(n int, fn func(int)) {
	if n <= 0 {
		return
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > n {
		workers = n
	}
	var idx int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := atomic.AddInt64(&idx, 1) - 1
				if i >= int64(n) {
					return
				}
				fn(int(i))
			}
		}()
	}
	wg.Wait()
}

func parallelFilter(masks []uint64, fn func(uint64) bool) []uint64 {
	ok := make([]bool, len(masks))
	parallelFor(len(masks), func(i int) { ok[i] = fn(masks[i]) })
	var out []uint64
	for i, m := range masks {
		if ok[i] {
			out = append(out, m)
		}
	}
	return out
}

// Solve finds integer prices satisfying all constraints, minimizing FIRST the
// number of changed items and ONLY THEN the total absolute adjustment
// (lexicographic order — never the other way around).
//
// Step 1 (primary): enumerate subsets of adjustable items from largest to
// smallest; the largest subset that can stay at its original price (checked
// with Bellman-Ford) determines the minimum number of changes.
// Step 2 (secondary): among all maximum kept sets, minimize the total
// absolute adjustment exactly via min-cost circulation.
func Solve(p Problem) Solution {
	n := p.N
	var lockedMask uint64
	var unlocked []int
	for i := 0; i < n; i++ {
		if p.Locked[i] {
			lockedMask |= 1 << uint(i)
		} else {
			unlocked = append(unlocked, i)
		}
	}
	u := len(unlocked)
	for k := u; k >= 0; k-- {
		masks := combinations(u, k)
		feas := parallelFilter(masks, func(mask uint64) bool {
			ok, _ := checkFeasible(p, expandMask(mask, unlocked, lockedMask))
			return ok
		})
		if len(feas) == 0 {
			continue
		}
		type res struct {
			mask   uint64
			dev    int64
			prices []int64
		}
		results := make([]res, len(feas))
		parallelFor(len(feas), func(i int) {
			dev, prices := optimize(p, expandMask(feas[i], unlocked, lockedMask))
			results[i] = res{feas[i], dev, prices}
		})
		best := results[0]
		for _, r := range results[1:] {
			if r.dev < best.dev || (r.dev == best.dev && r.mask < best.mask) {
				best = r
			}
		}
		sol := Solution{Feasible: true, Prices: best.prices, Deviation: best.dev}
		for i := 0; i < n; i++ {
			if best.prices[i] != p.A[i] {
				sol.Changed = append(sol.Changed, i)
			}
		}
		sol.Changes = len(sol.Changed)
		return sol
	}
	// Even with every adjustable item free, the locked prices conflict with
	// the constraints: report the negative cycle as proof.
	_, cyc := checkFeasible(p, lockedMask)
	var sum int64
	for _, e := range cyc {
		sum += e.w
	}
	return Solution{Feasible: false, Cycle: cyc, CycleSum: sum}
}

// ---- certificate rendering ----

func nodeName(p Problem, x int) string {
	if x == p.N {
		return "0"
	}
	return fmt.Sprintf("p%d", x+1)
}

func (e fEdge) inequality(p Problem) string {
	return fmt.Sprintf("%s - %s <= %d", nodeName(p, e.to), nodeName(p, e.from), e.w)
}

func boundStr(v int64, has bool, lower bool) string {
	if !has {
		if lower {
			return "-inf"
		}
		return "inf"
	}
	return fmt.Sprintf("%d", v)
}

func (e fEdge) source(p Problem) string {
	switch e.kind {
	case edgeConHi, edgeConLo:
		c := p.Cons[e.idx]
		return fmt.Sprintf("constraint #%d: %s <= p%d - p%d <= %s",
			e.idx+1, boundStr(c.Lo, c.HasLo, true), c.V+1, c.U+1, boundStr(c.Hi, c.HasHi, false))
	default:
		return fmt.Sprintf("item %d locked at original price %d", e.idx+1, p.A[e.idx])
	}
}
