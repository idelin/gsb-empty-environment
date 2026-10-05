package compensation

import (
	"fmt"
	"sort"
)

// Step describes one node in the dependency graph.
// Compensate may be nil, which means the step cannot be
// compensated automatically and must be handled manually.
type Step struct {
	ID         string
	DependsOn  []string
	Run        func(ctx Context) error
	Compensate func(ctx Context) error
}

// Context is the minimal context interface used by step functions.
// It is satisfied by context.Context.
type Context interface {
	Done() <-chan struct{}
	Err() error
}

// graph holds validated adjacency information for a set of steps.
type graph struct {
	steps    map[string]*Step
	children map[string][]string // dependents of each step
	indegree map[string]int
}

func buildGraph(steps []Step) (*graph, error) {
	g := &graph{
		steps:    make(map[string]*Step, len(steps)),
		children: make(map[string][]string),
		indegree: make(map[string]int),
	}
	for i := range steps {
		s := steps[i]
		if s.ID == "" {
			return nil, fmt.Errorf("compensation: step with empty ID")
		}
		if _, dup := g.steps[s.ID]; dup {
			return nil, fmt.Errorf("compensation: duplicate step ID %q", s.ID)
		}
		if s.Run == nil {
			return nil, fmt.Errorf("compensation: step %q has nil Run", s.ID)
		}
		cp := s
		g.steps[s.ID] = &cp
		g.indegree[s.ID] = 0
	}
	for id, s := range g.steps {
		for _, dep := range s.DependsOn {
			if dep == id {
				return nil, fmt.Errorf("compensation: step %q depends on itself", id)
			}
			if _, ok := g.steps[dep]; !ok {
				return nil, fmt.Errorf("compensation: step %q depends on unknown step %q", id, dep)
			}
			g.children[dep] = append(g.children[dep], id)
			g.indegree[id]++
		}
	}
	if _, err := g.topoOrder(); err != nil {
		return nil, err
	}
	return g, nil
}

// topoOrder returns the step IDs in topological (dependency-first) order.
// Order within a level is deterministic (sorted by ID).
func (g *graph) topoOrder() ([]string, error) {
	indeg := make(map[string]int, len(g.indegree))
	for id, d := range g.indegree {
		indeg[id] = d
	}
	var ready []string
	for id, d := range indeg {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		var next []string
		for _, child := range g.children[id] {
			indeg[child]--
			if indeg[child] == 0 {
				next = append(next, child)
			}
		}
		if len(next) > 0 {
			ready = append(ready, next...)
			sort.Strings(ready)
		}
	}
	if len(order) != len(g.steps) {
		return nil, fmt.Errorf("compensation: dependency graph contains a cycle")
	}
	return order, nil
}

// levels returns step IDs grouped by topological level: steps in the
// same level are independent of each other and may run in parallel.
func (g *graph) levels() [][]string {
	indeg := make(map[string]int, len(g.indegree))
	for id, d := range g.indegree {
		indeg[id] = d
	}
	var current []string
	for id, d := range indeg {
		if d == 0 {
			current = append(current, id)
		}
	}
	sort.Strings(current)
	var lv [][]string
	for len(current) > 0 {
		lv = append(lv, current)
		var next []string
		for _, id := range current {
			for _, child := range g.children[id] {
				indeg[child]--
				if indeg[child] == 0 {
					next = append(next, child)
				}
			}
		}
		sort.Strings(next)
		current = next
	}
	return lv
}

// compensationOrder returns the given completed step IDs ordered for
// compensation: exact reverse of topological order, so dependents are
// compensated before the steps they depend on.
func (g *graph) compensationOrder(completed map[string]bool) []string {
	order, err := g.topoOrder()
	if err != nil {
		return nil
	}
	var out []string
	for i := len(order) - 1; i >= 0; i-- {
		if completed[order[i]] {
			out = append(out, order[i])
		}
	}
	return out
}
