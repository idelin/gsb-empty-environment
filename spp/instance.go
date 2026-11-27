package spp

import (
	"fmt"
	"sort"
)

// Instance is a fully expanded Stable Paths Problem instance for one
// destination prefix: a topology, the origin, and a strict preference order
// over the legal candidate paths of every AS.
type Instance struct {
	Graph  *ASGraph
	Config PolicyConfig
	// Ranked[u] is the strict total order of u's permitted paths, best first.
	Ranked map[int][]PathRank
	Report ValidationReport

	index map[int]map[string]int // node -> path key -> rank
}

// Build expands every legal path, applies Gao-Rexford filtering and audits
// the commercial hierarchy. StrictGR only controls hard rejection/export
// simulation; validation diagnostics are always populated.
func Build(g *ASGraph, cfg PolicyConfig) (*Instance, error) {
	if g == nil {
		return nil, fmt.Errorf("spp: nil graph")
	}
	if !containsNode(g, cfg.Origin) {
		return nil, fmt.Errorf("spp: origin %d is not a graph node", cfg.Origin)
	}
	ranked, pathViolations := ExpandPaths(g, cfg)
	inst := &Instance{
		Graph:  g,
		Config: cfg,
		Ranked: ranked,
		index:  make(map[int]map[string]int, len(g.Nodes)),
	}
	inst.Report = Validate(g, cfg, ranked, pathViolations)
	for _, n := range g.Nodes {
		inst.index[n] = make(map[string]int, len(ranked[n]))
		for _, pr := range ranked[n] {
			inst.index[n][PathKey(pr.Path)] = pr.Rank
		}
	}
	return inst, nil
}

func containsNode(g *ASGraph, n int) bool {
	for _, x := range g.Nodes {
		if x == n {
			return true
		}
	}
	return false
}

// Paths returns the ordered permitted paths of node u (best first).
func (in *Instance) Paths(u int) []PathRank {
	return in.Ranked[u]
}

// RankOf returns the rank (lower is better) and presence of path p at u.
func (in *Instance) RankOf(u int, p []int) (int, bool) {
	m, ok := in.index[u]
	if !ok {
		return 0, false
	}
	r, ok := m[PathKey(p)]
	return r, ok
}

// Prefers reports whether u strictly prefers a over b.
func (in *Instance) Prefers(u int, a, b []int) (bool, error) {
	ra, oka := in.RankOf(u, a)
	rb, okb := in.RankOf(u, b)
	if !oka || !okb {
		return false, fmt.Errorf("spp: AS %d has no such ranked path (a=%v b=%v)", u, a, b)
	}
	return ra < rb, nil
}

// AvailablePaths returns u's permitted paths that extend a route currently
// advertised by a neighbor: the path's second hop must be a neighbor whose
// own selected route is the matching suffix. An empty neighbor assignment
// means that neighbor advertises nothing.
func (in *Instance) AvailablePaths(u int, assignment map[int][]int) [][]int {
	var out [][]int
	for _, pr := range in.Ranked[u] {
		p := pr.Path
		if len(p) < 2 {
			continue
		}
		next := p[1]
		selected, decided := assignment[next]
		if !decided {
			continue // neighbor has not decided yet: cannot advertise
		}
		if len(selected) == 0 {
			continue // decided to remain unrouted: advertises no route
		}
		if !pathEquals(selected, p[1:]) {
			continue // AS_PATH loop prevention: suffix must match
		}
		out = append(out, CopyPath(p))
	}
	return out
}

// BestAvailable returns the highest-ranked currently extendable path, or nil.
func (in *Instance) BestAvailable(u int, assignment map[int][]int) []int {
	avail := in.AvailablePaths(u, assignment)
	if len(avail) == 0 {
		return nil
	}
	best := avail[0]
	bestRank, _ := in.RankOf(u, best)
	for _, p := range avail[1:] {
		r, _ := in.RankOf(u, p)
		if r < bestRank {
			best, bestRank = p, r
		}
	}
	return best
}

func pathEquals(a, b []int) bool {
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

// SortedNodes returns nodes in ascending id for deterministic iteration.
func (in *Instance) SortedNodes() []int {
	out := append([]int(nil), in.Graph.Nodes...)
	sort.Ints(out)
	return out
}
