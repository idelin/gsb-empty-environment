package compensation

import (
	"context"
	"fmt"
	"sort"
)

// RunFunc executes the forward action of a step.
type RunFunc func(ctx context.Context) error

// CompensateFunc undoes the forward action of a step.
type CompensateFunc func(ctx context.Context) error

// Step is a node in the dependency graph. A step models an external
// action that may need to be compensated (rolled back) later.
type Step struct {
	ID        string
	DependsOn []string
	// Compensatable declares whether this step can be undone
	// automatically. Non-compensatable steps become manual items.
	Compensatable bool
	Run           RunFunc
	Compensate    CompensateFunc
}

// Graph is a directed acyclic graph of steps.
type Graph struct {
	steps map[string]*Step
}

// NewGraph returns an empty graph.
func NewGraph() *Graph {
	return &Graph{steps: make(map[string]*Step)}
}

// Add registers a step. It fails on duplicate IDs.
func (g *Graph) Add(s Step) error {
	if s.ID == "" {
		return fmt.Errorf("compensation: step ID must not be empty")
	}
	if _, ok := g.steps[s.ID]; ok {
		return fmt.Errorf("compensation: duplicate step ID %q", s.ID)
	}
	copied := s
	g.steps[copied.ID] = &copied
	return nil
}

// Step returns the step with the given ID, or nil.
func (g *Graph) Step(id string) *Step {
	return g.steps[id]
}

// topoOrder returns the steps in a deterministic topological order
// (dependencies first). It validates missing dependencies and cycles.
func (g *Graph) topoOrder() ([]*Step, error) {
	indegree := make(map[string]int, len(g.steps))
	dependents := make(map[string][]string, len(g.steps))
	for _, s := range g.steps {
		indegree[s.ID] = 0
	}
	for _, s := range g.steps {
		for _, dep := range s.DependsOn {
			if _, ok := g.steps[dep]; !ok {
				return nil, fmt.Errorf("compensation: step %q depends on unknown step %q", s.ID, dep)
			}
			indegree[s.ID]++
			dependents[dep] = append(dependents[dep], s.ID)
		}
	}

	var ready []string
	for id, d := range indegree {
		if d == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	order := make([]*Step, 0, len(g.steps))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, g.steps[id])
		next := dependents[id]
		sort.Strings(next)
		for _, dep := range next {
			indegree[dep]--
			if indegree[dep] == 0 {
				ready = append(ready, dep)
			}
		}
	}
	if len(order) != len(g.steps) {
		return nil, fmt.Errorf("compensation: dependency graph contains a cycle")
	}
	return order, nil
}
