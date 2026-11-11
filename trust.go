package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// SourceID identifies a credential origin, for example instance metadata,
// task identity, or an environment-injected credential.
type SourceID string

// TrustEdge declares that Higher is strictly more trusted than Lower.
type TrustEdge struct {
	Higher SourceID
	Lower  SourceID
}

// Order describes the trust relation between two sources.
type Order int

const (
	// Incomparable means neither source dominates the other.
	Incomparable Order = iota
	// Equal means both arguments name the same source.
	Equal
	// MoreTrusted means the first argument dominates the second.
	MoreTrusted
	// LessTrusted means the second argument dominates the first.
	LessTrusted
)

var (
	// ErrEmptySource is returned when a source id is the empty string.
	ErrEmptySource = errors.New("netsec: empty source id")
	// ErrTrustCycle is returned when the declared edges form a cycle, so
	// they cannot define a strict partial order.
	ErrTrustCycle = errors.New("netsec: trust graph contains a cycle")
)

// TrustGraph is an immutable directed strict partial order over credential
// sources. An edge Higher -> Lower means credentials from Higher must never
// be overridden by credentials from Lower.
type TrustGraph struct {
	nodes map[SourceID]struct{}
	// edges[a] holds the direct lower neighbours of a.
	edges map[SourceID]map[SourceID]struct{}
	// dom[a][b] is present when a transitively dominates b.
	dom map[SourceID]map[SourceID]struct{}
}

// NewTrustGraph builds a trust graph from the given nodes and directed edges.
// Isolated nodes are allowed: they are comparable only with themselves.
func NewTrustGraph(nodes []SourceID, edges []TrustEdge) (*TrustGraph, error) {
	g := &TrustGraph{
		nodes: make(map[SourceID]struct{}, len(nodes)),
		edges: make(map[SourceID]map[SourceID]struct{}),
		dom:   make(map[SourceID]map[SourceID]struct{}),
	}
	for _, node := range nodes {
		if node == "" {
			return nil, ErrEmptySource
		}
		g.nodes[node] = struct{}{}
	}
	for _, edge := range edges {
		if edge.Higher == "" || edge.Lower == "" {
			return nil, fmt.Errorf("netsec: trust edge with empty endpoint %q -> %q", edge.Higher, edge.Lower)
		}
		if edge.Higher == edge.Lower {
			return nil, fmt.Errorf("netsec: self trust edge on source %q", edge.Higher)
		}
		if _, ok := g.nodes[edge.Higher]; !ok {
			return nil, fmt.Errorf("netsec: trust edge references unknown source %q", edge.Higher)
		}
		if _, ok := g.nodes[edge.Lower]; !ok {
			return nil, fmt.Errorf("netsec: trust edge references unknown source %q", edge.Lower)
		}
		if g.edges[edge.Higher] == nil {
			g.edges[edge.Higher] = make(map[SourceID]struct{})
		}
		g.edges[edge.Higher][edge.Lower] = struct{}{}
	}
	if err := g.detectCycle(); err != nil {
		return nil, err
	}
	for node := range g.nodes {
		g.dom[node] = g.reachable(node)
	}
	return g, nil
}

// Contains reports whether id is a registered source.
func (g *TrustGraph) Contains(id SourceID) bool {
	_, ok := g.nodes[id]
	return ok
}

// Nodes returns the registered sources in deterministic sorted order.
func (g *TrustGraph) Nodes() []SourceID {
	out := make([]SourceID, 0, len(g.nodes))
	for node := range g.nodes {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Dominates reports whether higher transitively dominates lower through the
// declared trust edges.
func (g *TrustGraph) Dominates(higher, lower SourceID) bool {
	if higher == lower {
		return false
	}
	_, ok := g.dom[higher][lower]
	return ok
}

// Compare returns the trust relation between a and b. Unknown sources are
// reported as Incomparable.
func (g *TrustGraph) Compare(a, b SourceID) Order {
	if a == b {
		if g.Contains(a) {
			return Equal
		}
		return Incomparable
	}
	if g.Dominates(a, b) {
		return MoreTrusted
	}
	if g.Dominates(b, a) {
		return LessTrusted
	}
	return Incomparable
}

// Path returns the shortest directed trust path from higher to lower,
// including both endpoints. It returns nil when higher does not dominate
// lower (or when either source is unknown).
func (g *TrustGraph) Path(higher, lower SourceID) []SourceID {
	if !g.Contains(higher) || !g.Contains(lower) {
		return nil
	}
	if higher == lower {
		return []SourceID{higher}
	}
	prev := map[SourceID]SourceID{higher: ""}
	queue := []SourceID{higher}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range g.sortedLowers(current) {
			if _, seen := prev[next]; seen {
				continue
			}
			prev[next] = current
			if next == lower {
				path := []SourceID{}
				for node := lower; node != ""; node = prev[node] {
					path = append([]SourceID{node}, path...)
				}
				return path
			}
			queue = append(queue, next)
		}
	}
	return nil
}

func (g *TrustGraph) detectCycle() error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[SourceID]int, len(g.nodes))
	stack := make([]SourceID, 0, len(g.nodes))
	var visit func(node SourceID) error
	visit = func(node SourceID) error {
		color[node] = gray
		stack = append(stack, node)
		for _, next := range g.sortedLowers(node) {
			switch color[next] {
			case white:
				if err := visit(next); err != nil {
					return err
				}
			case gray:
				start := 0
				for stack[start] != next {
					start++
				}
				cycle := append([]SourceID{}, stack[start:]...)
				cycle = append(cycle, next)
				return fmt.Errorf("%w: %v", ErrTrustCycle, cycle)
			}
		}
		stack = stack[:len(stack)-1]
		color[node] = black
		return nil
	}
	for _, node := range g.sortedNodes() {
		if color[node] == white {
			if err := visit(node); err != nil {
				return err
			}
		}
	}
	return nil
}

func (g *TrustGraph) reachable(from SourceID) map[SourceID]struct{} {
	seen := make(map[SourceID]struct{})
	queue := []SourceID{from}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range g.sortedLowers(current) {
			if _, ok := seen[next]; ok {
				continue
			}
			seen[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return seen
}

func (g *TrustGraph) sortedNodes() []SourceID {
	out := make([]SourceID, 0, len(g.nodes))
	for node := range g.nodes {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (g *TrustGraph) sortedLowers(node SourceID) []SourceID {
	out := make([]SourceID, 0, len(g.edges[node]))
	for next := range g.edges[node] {
		out = append(out, next)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
