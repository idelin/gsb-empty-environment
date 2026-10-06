package retention

import (
	"fmt"
	"sort"
)

// Graph is the recovery graph over the backup catalog. It is used to
// validate dependency integrity and to compute dependency closures.
type Graph struct {
	points map[string]Point
	sorted []Point // all points, ascending by time (ID tie-break)
}

// NewGraph builds a recovery graph, rejecting duplicate IDs.
func NewGraph(points []Point) (*Graph, error) {
	g := &Graph{points: make(map[string]Point, len(points))}
	for _, p := range points {
		if p.ID == "" {
			return nil, fmt.Errorf("backup point with empty ID")
		}
		if _, dup := g.points[p.ID]; dup {
			return nil, fmt.Errorf("duplicate backup point ID %q", p.ID)
		}
		g.points[p.ID] = p
		g.sorted = append(g.sorted, p)
	}
	sort.Slice(g.sorted, func(i, j int) bool {
		a, b := g.sorted[i], g.sorted[j]
		if a.Time.Equal(b.Time) {
			return a.ID < b.ID
		}
		return a.Time.Before(b.Time)
	})
	return g, nil
}

// Get returns a point by ID.
func (g *Graph) Get(id string) (Point, bool) {
	p, ok := g.points[id]
	return p, ok
}

// Points returns all points in ascending time order.
func (g *Graph) Points() []Point {
	out := make([]Point, len(g.sorted))
	copy(out, g.sorted)
	return out
}

// Issue describes a single integrity problem found by Validate.
type Issue struct {
	PointID string
	Kind    string
	Detail  string
}

func (i Issue) String() string {
	return fmt.Sprintf("[%s] %s: %s", i.Kind, i.PointID, i.Detail)
}

// Validate checks the structural integrity of the recovery graph:
// dependency references exist, dependency timestamps precede the
// dependent, full backups carry no dependencies, incrementals carry at
// least one, the graph is acyclic, and corrupt points are surfaced.
func (g *Graph) Validate() []Issue {
	var issues []Issue
	for _, p := range g.sorted {
		switch p.Type {
		case Full:
			if len(p.DependsOn) > 0 {
				issues = append(issues, Issue{p.ID, "full-with-deps",
					"full backup must not declare dependencies"})
			}
		case Incremental:
			if len(p.DependsOn) == 0 {
				issues = append(issues, Issue{p.ID, "incremental-without-deps",
					"incremental backup must depend on a baseline or prior incremental"})
			}
		}
		if p.Corrupt {
			reason := p.CorruptReason
			if reason == "" {
				reason = "integrity check failed"
			}
			issues = append(issues, Issue{p.ID, "corrupt", reason})
		}
		for _, dep := range p.DependsOn {
			d, ok := g.points[dep]
			if !ok {
				issues = append(issues, Issue{p.ID, "missing-dependency",
					fmt.Sprintf("dependency %q not found in catalog", dep)})
				continue
			}
			if !d.Time.Before(p.Time) {
				issues = append(issues, Issue{p.ID, "time-order",
					fmt.Sprintf("dependency %q (%s) is not older than dependent (%s)",
						dep, d.Time.Format("2006-01-02 15:04"), p.Time.Format("2006-01-02 15:04"))})
			}
		}
	}
	issues = append(issues, g.cycleIssues()...)
	return issues
}

func (g *Graph) cycleIssues() []Issue {
	const (
		white = 0 // unvisited
		gray  = 1 // on stack
		black = 2 // done
	)
	color := make(map[string]int, len(g.points))
	var issues []Issue
	var visit func(id string, stack []string)
	visit = func(id string, stack []string) {
		color[id] = gray
		stack = append(stack, id)
		for _, dep := range g.points[id].DependsOn {
			if _, ok := g.points[dep]; !ok {
				continue
			}
			switch color[dep] {
			case gray:
				issues = append(issues, Issue{id, "cycle",
					fmt.Sprintf("dependency cycle detected: %v -> %s", stack, dep)})
			case white:
				visit(dep, stack)
			}
		}
		color[id] = black
	}
	for _, p := range g.sorted {
		if color[p.ID] == white {
			visit(p.ID, nil)
		}
	}
	return issues
}

// Closure returns the set of point IDs that must be retained to restore
// every point in ids: the points themselves plus all transitive
// dependencies. Missing dependency IDs are ignored here; they are
// reported by Validate and Usable instead.
func (g *Graph) Closure(ids []string) map[string]bool {
	keep := make(map[string]bool)
	var walk func(id string)
	walk = func(id string) {
		if keep[id] {
			return
		}
		keep[id] = true
		p, ok := g.points[id]
		if !ok {
			return
		}
		for _, dep := range p.DependsOn {
			walk(dep)
		}
	}
	for _, id := range ids {
		walk(id)
	}
	return keep
}

// Chain returns the dependency chain of id ordered from the full
// baseline up to the point itself. Missing dependencies are skipped.
func (g *Graph) Chain(id string) []Point {
	var chain []Point
	seen := make(map[string]bool)
	var walk func(pid string)
	walk = func(pid string) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		p, ok := g.points[pid]
		if !ok {
			return
		}
		for _, dep := range p.DependsOn {
			walk(dep)
		}
		chain = append(chain, p)
	}
	walk(id)
	return chain
}

// Usable reports whether id can actually be restored: the point exists,
// is not corrupt, and its entire transitive dependency chain exists and
// is uncorrupted. A broken dependency never causes a point to be
// silently dropped; it is reported here as a human-readable problem.
func (g *Graph) Usable(id string) (bool, []string) {
	memo := make(map[string][]string)
	visiting := make(map[string]bool)
	var problems func(pid string) []string
	problems = func(pid string) []string {
		if probs, ok := memo[pid]; ok {
			return probs
		}
		if visiting[pid] {
			return []string{fmt.Sprintf("dependency cycle involving %q", pid)}
		}
		visiting[pid] = true
		p, ok := g.points[pid]
		if !ok {
			probs := []string{fmt.Sprintf("dependency %q is missing from the catalog", pid)}
			memo[pid] = probs
			return probs
		}
		var probs []string
		if p.Corrupt {
			reason := p.CorruptReason
			if reason == "" {
				reason = "integrity check failed"
			}
			probs = append(probs, fmt.Sprintf("%q is corrupt: %s", pid, reason))
		}
		for _, dep := range p.DependsOn {
			for _, sub := range problems(dep) {
				probs = append(probs, fmt.Sprintf("via %q: %s", pid, sub))
			}
		}
		visiting[pid] = false
		memo[pid] = probs
		return probs
	}
	probs := problems(id)
	return len(probs) == 0, probs
}
