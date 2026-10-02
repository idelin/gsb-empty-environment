// Package engine implements a spreadsheet-like formula dependency graph
// with incremental topological re-evaluation and cycle detection.
package engine

import (
	"fmt"
	"sort"
	"strings"
)

func sortStrings(s []string) { sort.Strings(s) }

// Cell is a single node in the dependency graph.
type Cell struct {
	Name    string
	Formula string
	Value   float64

	hasFormula bool
	ast        node
	deps       map[string]bool // cells this cell depends on (incoming edges)
	dependents map[string]bool // cells that depend on this cell (outgoing edges)
	evalCount  int             // number of times the formula has been evaluated
}

// CycleError is returned when a formula would introduce a direct or
// indirect circular reference. Path holds the full cycle, e.g.
// ["A1", "C1", "B1", "A1"].
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("circular reference detected: %s", strings.Join(e.Path, " -> "))
}

// Engine is the dependency-graph evaluation engine.
type Engine struct {
	cells map[string]*Cell
}

// New creates an empty Engine.
func New() *Engine {
	return &Engine{cells: map[string]*Cell{}}
}

func normalize(name string) string { return strings.ToUpper(strings.TrimSpace(name)) }

func (e *Engine) getOrCreate(name string) *Cell {
	name = normalize(name)
	c, ok := e.cells[name]
	if !ok {
		c = &Cell{
			Name:       name,
			deps:       map[string]bool{},
			dependents: map[string]bool{},
		}
		e.cells[name] = c
	}
	return c
}

// Get returns the current value of a cell.
func (e *Engine) Get(name string) (float64, error) {
	c, ok := e.cells[normalize(name)]
	if !ok {
		return 0, fmt.Errorf("undefined cell %s", normalize(name))
	}
	return c.Value, nil
}

// EvalCount returns how many times the cell's formula has been evaluated.
// Useful for verifying that recomputation stays inside the affected subgraph.
func (e *Engine) EvalCount(name string) int {
	if c, ok := e.cells[normalize(name)]; ok {
		return c.evalCount
	}
	return 0
}

// SetValue assigns a literal value to a cell, clearing any formula it had,
// then incrementally recomputes only the affected downstream subgraph.
func (e *Engine) SetValue(name string, v float64) error {
	c := e.getOrCreate(name)
	if c.hasFormula {
		for d := range c.deps {
			delete(e.cells[d].dependents, c.Name)
		}
		c.deps = map[string]bool{}
		c.hasFormula = false
		c.Formula = ""
		c.ast = nil
	}
	c.Value = v
	return e.recomputeFrom(c.Name)
}

// SetFormula parses and installs a formula on a cell. Before committing,
// it verifies the formula would not create a circular reference; if it
// would, a *CycleError carrying the full cycle path is returned and the
// engine state is left completely untouched (the cycle is isolated).
func (e *Engine) SetFormula(name, formula string) error {
	name = normalize(name)
	ast, refs, err := compileFormula(formula)
	if err != nil {
		return fmt.Errorf("invalid formula for %s: %v", name, err)
	}
	for _, r := range refs {
		if _, ok := e.cells[r]; !ok {
			return fmt.Errorf("invalid formula for %s: undefined cell %s", name, r)
		}
	}
	// Cycle check: adding edges name<-ref for each ref creates a cycle iff
	// `name` is already reachable from some ref via depends-on edges.
	for _, r := range refs {
		if path := e.dependsPath(r, name); path != nil {
			full := append([]string{name}, path...)
			return &CycleError{Path: full}
		}
	}
	// Commit: rewire edges, then recompute this cell and its downstream.
	c := e.getOrCreate(name)
	if c.hasFormula {
		for d := range c.deps {
			delete(e.cells[d].dependents, name)
		}
	}
	c.deps = map[string]bool{}
	for _, r := range refs {
		c.deps[r] = true
		e.cells[r].dependents[name] = true
	}
	c.hasFormula = true
	c.Formula = formula
	c.ast = ast
	return e.recomputeFrom(name)
}

// dependsPath returns a chain of depends-on edges from `from` to `to`
// (inclusive, e.g. [C1, B1, A1] meaning C1 depends on B1 depends on A1),
// or nil if `to` is not reachable from `from`.
func (e *Engine) dependsPath(from, to string) []string {
	if from == to {
		return []string{from}
	}
	visited := map[string]bool{from: true}
	var dfs func(cur string) []string
	dfs = func(cur string) []string {
		deps := make([]string, 0, len(e.cells[cur].deps))
		for d := range e.cells[cur].deps {
			deps = append(deps, d)
		}
		sortStrings(deps)
		for _, d := range deps {
			if d == to {
				return []string{cur, d}
			}
			if visited[d] {
				continue
			}
			visited[d] = true
			if sub := dfs(d); sub != nil {
				return append([]string{cur}, sub...)
			}
		}
		return nil
	}
	return dfs(from)
}

// recomputeFrom incrementally re-evaluates only the cells downstream of
// `root` (plus root itself if it holds a formula), in topological order,
// using Kahn's algorithm over the affected subgraph.
func (e *Engine) recomputeFrom(root string) error {
	// 1. Collect the affected subgraph: everything reachable from root
	//    through dependents edges that carries a formula.
	affected := map[string]bool{}
	seen := map[string]bool{root: true}
	queue := []string{root}
	if e.cells[root].hasFormula {
		affected[root] = true
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for dep := range e.cells[n].dependents {
			if !seen[dep] {
				seen[dep] = true
				queue = append(queue, dep)
			}
			if e.cells[dep].hasFormula {
				affected[dep] = true
			}
		}
	}
	if len(affected) == 0 {
		return nil
	}
	// 2. Kahn's topological sort restricted to the affected subgraph.
	indeg := map[string]int{}
	for n := range affected {
		for d := range e.cells[n].deps {
			if affected[d] {
				indeg[n]++
			}
		}
	}
	var ready []string
	for n := range affected {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	sortStrings(ready) // deterministic evaluation order
	// 3. Evaluate in topological order; dependencies are always computed
	//    before their dependents.
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		c := e.cells[n]
		v, err := c.ast.eval(func(ref string) (float64, error) {
			return e.cells[ref].Value, nil
		})
		if err != nil {
			return fmt.Errorf("evaluating %s: %v", n, err)
		}
		c.Value = v
		c.evalCount++
		var newlyReady []string
		for dep := range c.dependents {
			if affected[dep] {
				indeg[dep]--
				if indeg[dep] == 0 {
					newlyReady = append(newlyReady, dep)
				}
			}
		}
		if len(newlyReady) > 0 {
			sortStrings(newlyReady)
			ready = append(ready, newlyReady...)
		}
	}
	return nil
}
