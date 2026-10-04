package main

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Edge is an undirected weighted tree edge.
type Edge struct {
	U int     `json:"u"`
	V int     `json:"v"`
	W float64 `json:"w"`
}

// Input describes one facility-location instance on a tree.
type Input struct {
	N            int     `json:"n"`
	K            int     `json:"k"`
	Edges        []Edge  `json:"edges"`
	Demands      []int64 `json:"demands"`
	Forbidden    []int   `json:"forbidden"`
	TimeBudgetMs int     `json:"time_budget_ms"`
}

// Output is the solver result. TotalCost is null when infeasible.
type Output struct {
	Status      string   `json:"status"` // "optimal", "feasible" or "infeasible"
	Facilities  []int    `json:"facilities"`
	Assignments []int    `json:"assignments"` // facility node serving each node, -1 if none
	TotalCost   *float64 `json:"total_cost"`
	Optimal     bool     `json:"optimal"`
}

func costPtr(c float64) *float64 { return &c }

const (
	defaultBudgetMs    = 2000
	defaultMaxExactEnt = 4000000 // n*n*(K+1) DP states cap for the exact solver
)

type options struct {
	budgetMs        int
	forceHeuristic  bool
	maxExactEntries int
}

// tree is the rooted (at 0) adjacency representation shared by solvers.
type tree struct {
	n        int
	adj      [][]nbr
	children [][]int
	parent   []int
	pw       []float64 // weight to parent
	order    []int     // post-order (children before parents)
	tin      []int
	tout     []int
}

type nbr struct {
	to int
	w  float64
}

func validate(in *Input) error {
	if in.N < 1 {
		return errors.New("n must be >= 1")
	}
	if in.K < 0 {
		return errors.New("k must be >= 0")
	}
	if len(in.Edges) != in.N-1 {
		return fmt.Errorf("expected %d edges, got %d", in.N-1, len(in.Edges))
	}
	if len(in.Demands) != in.N {
		return fmt.Errorf("expected %d demands, got %d", in.N, len(in.Demands))
	}
	for i, d := range in.Demands {
		if d < 0 {
			return fmt.Errorf("demand[%d] is negative", i)
		}
	}
	seen := make(map[int]bool)
	for _, f := range in.Forbidden {
		if f < 0 || f >= in.N {
			return fmt.Errorf("forbidden node %d out of range", f)
		}
		seen[f] = true
	}
	for i, e := range in.Edges {
		if e.U < 0 || e.U >= in.N || e.V < 0 || e.V >= in.N {
			return fmt.Errorf("edge %d endpoint out of range", i)
		}
		if e.U == e.V {
			return fmt.Errorf("edge %d is a self-loop", i)
		}
		if e.W < 0 || math.IsNaN(e.W) || math.IsInf(e.W, 0) {
			return fmt.Errorf("edge %d has invalid weight", i)
		}
	}
	// connectivity / tree check via DSU
	dsu := newDSU(in.N)
	for _, e := range in.Edges {
		if !dsu.union(e.U, e.V) {
			return errors.New("edges contain a cycle; not a tree")
		}
	}
	if dsu.count != 1 {
		return errors.New("edges do not connect all nodes; not a tree")
	}
	return nil
}

type dsu struct {
	p     []int
	count int
}

func newDSU(n int) *dsu {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &dsu{p: p, count: n}
}

func (d *dsu) find(x int) int {
	for d.p[x] != x {
		d.p[x] = d.p[d.p[x]]
		x = d.p[x]
	}
	return x
}

func (d *dsu) union(a, b int) bool {
	ra, rb := d.find(a), d.find(b)
	if ra == rb {
		return false
	}
	d.p[ra] = rb
	d.count--
	return true
}

func buildTree(in *Input) *tree {
	t := &tree{n: in.N}
	t.adj = make([][]nbr, in.N)
	for _, e := range in.Edges {
		t.adj[e.U] = append(t.adj[e.U], nbr{e.V, e.W})
		t.adj[e.V] = append(t.adj[e.V], nbr{e.U, e.W})
	}
	t.children = make([][]int, in.N)
	t.parent = make([]int, in.N)
	t.pw = make([]float64, in.N)
	t.tin = make([]int, in.N)
	t.tout = make([]int, in.N)
	for i := range t.parent {
		t.parent[i] = -1
	}
	// iterative DFS from root 0 producing post-order and tin/tout
	timer := 0
	type frame struct {
		v, idx int
	}
	stack := []frame{{0, 0}}
	t.parent[0] = -2 // marks visited root
	t.tin[0] = timer
	timer++
	var order []int
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		v := top.v
		if top.idx < len(t.adj[v]) {
			nb := t.adj[v][top.idx]
			top.idx++
			if t.parent[nb.to] == -1 {
				t.parent[nb.to] = v
				t.pw[nb.to] = nb.w
				t.children[v] = append(t.children[v], nb.to)
				t.tin[nb.to] = timer
				timer++
				stack = append(stack, frame{nb.to, 0})
			}
		} else {
			t.tout[v] = timer - 1
			order = append(order, v)
			stack = stack[:len(stack)-1]
		}
	}
	t.order = order
	return t
}

func (t *tree) inSubtree(anc, v int) bool {
	return t.tin[anc] <= t.tin[v] && t.tin[v] <= t.tout[anc]
}

// solve dispatches to the exact DP or the budget-limited heuristic.
func solve(in *Input, opt options) (Output, error) {
	if err := validate(in); err != nil {
		return Output{}, err
	}
	if opt.budgetMs <= 0 {
		opt.budgetMs = in.TimeBudgetMs
	}
	if opt.budgetMs <= 0 {
		opt.budgetMs = defaultBudgetMs
	}
	if opt.maxExactEntries <= 0 {
		opt.maxExactEntries = defaultMaxExactEnt
	}
	allowed := make([]bool, in.N)
	for i := range allowed {
		allowed[i] = true
	}
	for _, f := range in.Forbidden {
		allowed[f] = false
	}
	allowedCount := 0
	for _, a := range allowed {
		if a {
			allowedCount++
		}
	}
	var totalDemand int64
	for _, d := range in.Demands {
		totalDemand += d
	}

	// Degenerate cases: no facility can or must be placed.
	if allowedCount == 0 || in.K == 0 {
		if totalDemand == 0 {
			return Output{
				Status:      "optimal",
				Facilities:  []int{},
				Assignments: repeatInt(-1, in.N),
				TotalCost:   costPtr(0),
				Optimal:     true,
			}, nil
		}
		return Output{
			Status:      "infeasible",
			Facilities:  []int{},
			Assignments: repeatInt(-1, in.N),
			TotalCost:   nil,
			Optimal:     false,
		}, nil
	}
	if totalDemand == 0 {
		// Any allowed placement costs 0; placing nothing is simplest.
		return Output{
			Status:      "optimal",
			Facilities:  []int{},
			Assignments: repeatInt(-1, in.N),
			TotalCost:   costPtr(0),
			Optimal:     true,
		}, nil
	}

	t := buildTree(in)
	demands := make([]float64, in.N)
	for i, d := range in.Demands {
		demands[i] = float64(d)
	}

	kCap := in.K
	if kCap > allowedCount {
		kCap = allowedCount
	}
	entries := int64(in.N) * int64(in.N) * int64(kCap+1)
	if !opt.forceHeuristic && entries <= int64(opt.maxExactEntries) {
		return solveExact(in, t, demands, allowed, kCap), nil
	}
	budget := time.Duration(opt.budgetMs) * time.Millisecond
	return solveHeuristic(in, t, demands, allowed, kCap, budget), nil
}

func repeatInt(v, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// assignFromFacilities computes, for every node, the nearest facility
// (ties broken by smaller facility node id) and the total cost.
// dist must be the full all-pairs distance matrix.
func assignFromFacilities(dist [][]float64, demands []float64, facilities []int) ([]int, float64) {
	n := len(dist)
	assign := repeatInt(-1, n)
	if len(facilities) == 0 {
		return assign, 0
	}
	sorted := append([]int(nil), facilities...)
	// facilities scanned in ascending id order; strict "<" keeps smaller id on ties
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	total := 0.0
	for v := 0; v < n; v++ {
		best := -1
		bestD := math.Inf(1)
		for _, f := range sorted {
			if d := dist[v][f]; d < bestD {
				bestD = d
				best = f
			}
		}
		assign[v] = best
		total += demands[v] * bestD
	}
	return assign, total
}
