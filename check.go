package main

import (
	"fmt"
	"sort"
)

// Edge is a conditional dependency edge: From -> To active iff When evaluates true.
type Edge struct {
	From string
	To   string
	When string
}

// Graph is the input: named switches plus conditional edges.
type Graph struct {
	Switches []string
	Edges    []Edge
}

// Result of the exhaustive check.
type Result struct {
	Cyclic       bool     // some configuration makes the graph cyclic
	BestConfig   []bool   // lexicographically smallest cyclic bit string (switch order)
	Cycle        []string // a real cycle under BestConfig, first node repeated at end
	TotalConfigs uint64   // total configurations enumerated (2^len(Switches))
}

const maxSwitches = 10

// compiledEdge is an edge with its parsed condition.
type compiledEdge struct {
	from, to string
	when     Expr
}

// Check enumerates every switch configuration (at most 2^10), evaluates each
// edge's condition under that single configuration, builds the concrete graph,
// and tests acyclicity with a topological sort. Because each configuration is
// checked independently, edges guarded by mutually exclusive conditions can
// never be combined into a fake cycle.
func Check(g Graph) (Result, error) {
	if len(g.Switches) > maxSwitches {
		return Result{}, fmt.Errorf("too many switches: %d (max %d)", len(g.Switches), maxSwitches)
	}
	seen := map[string]bool{}
	for _, s := range g.Switches {
		if s == "" {
			return Result{}, fmt.Errorf("empty switch name")
		}
		if seen[s] {
			return Result{}, fmt.Errorf("duplicate switch %q", s)
		}
		seen[s] = true
	}
	for _, e := range g.Edges {
		if e.From == "" || e.To == "" {
			return Result{}, fmt.Errorf("edge with empty endpoint")
		}
	}

	compiled := make([]compiledEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		ex, err := ParseExpr(e.When)
		if err != nil {
			return Result{}, fmt.Errorf("edge %s -> %s: %v", e.From, e.To, err)
		}
		compiled = append(compiled, compiledEdge{e.From, e.To, ex})
	}

	n := len(g.Switches)
	total := uint64(1) << uint(n)
	res := Result{TotalConfigs: total}

	// Enumerate masks in increasing order; switch i maps to bit (n-1-i) so
	// mask order equals lexicographic order of the bit string b0 b1 ... b(n-1).
	for mask := uint64(0); mask < total; mask++ {
		env := make(map[string]bool, n)
		bits := make([]bool, n)
		for i := 0; i < n; i++ {
			b := (mask>>uint(n-1-i))&1 == 1
			env[g.Switches[i]] = b
			bits[i] = b
		}
		adj := buildGraph(compiled, env)
		if cyc := findCycle(adj); cyc != nil {
			res.Cyclic = true
			res.BestConfig = bits
			res.Cycle = cyc
			return res, nil
		}
	}
	return res, nil
}

// buildGraph evaluates every edge condition under env and returns the concrete
// adjacency list (nodes sorted for deterministic output).
func buildGraph(edges []compiledEdge, env map[string]bool) map[string][]string {
	adj := map[string][]string{}
	for _, e := range edges {
		if _, ok := adj[e.from]; !ok {
			adj[e.from] = nil
		}
		if _, ok := adj[e.to]; !ok {
			adj[e.to] = nil
		}
		if e.when.Eval(env) {
			adj[e.from] = append(adj[e.from], e.to)
		}
	}
	for k := range adj {
		sort.Strings(adj[k])
	}
	return adj
}

// findCycle first confirms a cycle exists via Kahn's topological sort, then
// extracts one concrete cycle by DFS restricted to the nodes that survive the
// topological elimination. Returns nil when the graph is acyclic.
func findCycle(adj map[string][]string) []string {
	indeg := map[string]int{}
	for v := range adj {
		if _, ok := indeg[v]; !ok {
			indeg[v] = 0
		}
	}
	for _, outs := range adj {
		for _, w := range outs {
			indeg[w]++
		}
	}
	var queue []string
	for v, d := range indeg {
		if d == 0 {
			queue = append(queue, v)
		}
	}
	sort.Strings(queue)
	removed := map[string]bool{}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		removed[v] = true
		for _, w := range adj[v] {
			indeg[w]--
			if indeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	if len(removed) == len(adj) {
		return nil // topological sort consumed everything: DAG
	}

	// Extract a real cycle among the surviving (cyclic) nodes.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var cycle []string
	var dfs func(v string) bool
	dfs = func(v string) bool {
		color[v] = gray
		stack = append(stack, v)
		for _, w := range adj[v] {
			if removed[w] {
				continue
			}
			if color[w] == gray {
				// back edge: slice the stack from w to the top
				idx := 0
				for i, s := range stack {
					if s == w {
						idx = i
						break
					}
				}
				cycle = append(cycle, stack[idx:]...)
				cycle = append(cycle, w)
				return true
			}
			if color[w] == white && dfs(w) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[v] = black
		return false
	}
	var survivors []string
	for v := range adj {
		if !removed[v] {
			survivors = append(survivors, v)
		}
	}
	sort.Strings(survivors)
	for _, v := range survivors {
		if color[v] == white && dfs(v) {
			return cycle
		}
	}
	return nil
}

// BitString renders a configuration as a 0/1 string in switch order.
func BitString(bits []bool) string {
	b := make([]byte, len(bits))
	for i, v := range bits {
		if v {
			b[i] = '1'
		} else {
			b[i] = '0'
		}
	}
	return string(b)
}
