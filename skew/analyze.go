package skew

import (
	"fmt"
	"sort"
)

// DefaultWide is the bound reported for offsets that are unconstrained
// relative to the reference node (no causal path either direction).
const DefaultWide = 365 * 24 * 60 * 60 * 1_000_000_000 // 1 year, ns

// Options controls an analysis. Wide is the absolute bound used for
// unconstrained offsets; non-positive values select DefaultWide.
type Options struct {
	Wide int64
}

// Interval is the feasible, closed interval [Lo, Hi] of a node's offset
// relative to the reference node. Bounded=false means the side is open:
// the value is only known to be <= Hi (Lo open) or >= Lo (Hi open).
type Interval struct {
	Node   string
	Lo     int64
	Hi     int64
	LoOpen bool
	HiOpen bool
}

// ChainEdge is one causal edge inside a conflict chain, shown with the
// nodes and observed local timestamps that produced its (signed) weight.
type ChainEdge struct {
	From   string
	To     string
	FromTS int64
	ToTS   int64
	Weight int64 // FromTS - ToTS; never clamped, a negative Weight is legal
}

// Chain is a closed, infeasible causal walk. Sum<0 is the contradiction:
// following the causal edges backwards-in-time would force x < x.
type Chain struct {
	Edges []ChainEdge
	Sum   int64
}

// Result is the outcome of an analysis.
type Result struct {
	Feasible  bool
	Reference string
	Wide      int64
	Intervals map[string]Interval
	// Assign is one feasible offset per node (x_R=0). CorrectedTS uses it.
	Assign map[string]int64
	// MinSlack is the minimum (corrected ToTS - corrected FromTS) over all
	// edges, including implicit local-order edges. Non-negative when feasible.
	MinSlack int64
	// Chains holds the contradiction chains when infeasible.
	Chains []Chain
}

// CorrectedTS returns the corrected (reference-frame) timestamp of an
// event: observed local timestamp plus the node's assigned offset.
func (r *Result) CorrectedTS(e Event) int64 {
	return addSat(e.TS, r.Assign[e.Node])
}

// cedge is one difference constraint. Causal edge u@tu -> v@tv with
// delta = tv - tu (signed, never clamped) requires x_u - x_v <= delta,
// which is the constraint-graph edge v -> u with weight delta.
type cedge struct {
	src, dst int // constraint-graph endpoints: src=node(v), dst=node(u)

	uNode string // causal source event node (u)
	vNode string // causal target event node (v)
	uTS   int64
	vTS   int64
	w     int64 // delta = vTS - uTS, signed; never clamped
}

// Analyze derives per-node feasible offset intervals. reference is the
// node whose offset is pinned to zero.
func (g *Graph) Analyze(reference string, opts Options) (*Result, error) {
	if err := g.index(); err != nil {
		return nil, err
	}
	wide := opts.Wide
	if wide <= 0 {
		wide = DefaultWide
	}
	ref, ok := g.nodeAt[reference]
	if !ok {
		return nil, fmt.Errorf("skew: reference node %q is unknown", reference)
	}

	cedges, err := g.resolveEdges()
	if err != nil {
		return nil, err
	}

	dist, neg := bellmanFordVirtual(len(g.nodes), cedges)
	if len(neg) > 0 {
		return &Result{Feasible: false, Reference: reference, Wide: wide, Chains: neg}, nil
	}

	fwd := bellmanFord(len(g.nodes), cedges, ref)
	rev := bellmanFordReversed(len(g.nodes), cedges, ref)

	intervals := make(map[string]Interval, len(g.nodes))
	assign := make(map[string]int64, len(g.nodes))
	for i, n := range g.nodes {
		if i == ref {
			intervals[n] = Interval{Node: n, Lo: 0, Hi: 0}
			assign[n] = 0
			continue
		}
		upper, hiOpen := fwd[i], fwd[i] == maxDist
		lower, loOpen := subSat(0, rev[i]), rev[i] == maxDist
		if loOpen {
			lower = -wide
		}
		if hiOpen {
			upper = wide
		}
		intervals[n] = Interval{Node: n, Lo: lower, Hi: upper, LoOpen: loOpen, HiOpen: hiOpen}
		// Virtual-source labels satisfy x_dst - x_src <= w; shifting by
		// the reference label pins x_R = 0 and stays feasible.
		assign[n] = subSat(dist[i], dist[ref])
	}

	minSlack := maxDist
	for _, e := range cedges {
		s := edgeSlack(e, assign)
		if s < minSlack {
			minSlack = s
		}
	}
	if minSlack == maxDist {
		minSlack = 0
	}

	return &Result{
		Feasible:  true,
		Reference: reference,
		Wide:      wide,
		Intervals: intervals,
		Assign:    assign,
		MinSlack:  minSlack,
	}, nil
}

func (g *Graph) index() error {
	g.nodeAt = make(map[string]int)
	g.event = make(map[string]Event, len(g.Events))
	seenEvents := make(map[string]bool, len(g.Events))
	for _, e := range g.Events {
		if e.ID == "" {
			return fmt.Errorf("skew: event has empty ID")
		}
		if seenEvents[e.ID] {
			return fmt.Errorf("skew: duplicate event ID %q", e.ID)
		}
		seenEvents[e.ID] = true
		if e.Node == "" {
			return fmt.Errorf("skew: event %q has empty node", e.ID)
		}
		if _, ok := g.nodeAt[e.Node]; !ok {
			g.nodeAt[e.Node] = len(g.nodes)
			g.nodes = append(g.nodes, e.Node)
		}
		g.event[e.ID] = e
	}
	for _, ed := range g.Edges {
		if ed.From == "" || ed.To == "" {
			return fmt.Errorf("skew: edge has empty endpoint")
		}
		if _, ok := g.event[ed.From]; !ok {
			return fmt.Errorf("skew: edge references unknown event %q", ed.From)
		}
		if _, ok := g.event[ed.To]; !ok {
			return fmt.Errorf("skew: edge references unknown event %q", ed.To)
		}
	}
	return nil
}

// resolveEdges turns causal event edges into node-level difference
// constraints and adds implicit local-order edges between consecutive
// observed events on every node.
func (g *Graph) resolveEdges() ([]cedge, error) {
	byNode := make(map[string][]Event)
	for _, e := range g.Events {
		byNode[e.Node] = append(byNode[e.Node], e)
	}
	for n := range byNode {
		sort.Slice(byNode[n], func(i, j int) bool {
			if byNode[n][i].TS != byNode[n][j].TS {
				return byNode[n][i].TS < byNode[n][j].TS
			}
			return byNode[n][i].ID < byNode[n][j].ID
		})
	}

	var out []cedge
	add := func(u, v Event) {
		out = append(out, cedge{
			src:   g.nodeAt[v.Node],
			dst:   g.nodeAt[u.Node],
			uNode: u.Node,
			vNode: v.Node,
			uTS:   u.TS,
			vTS:   v.TS,
			w:     subSat(v.TS, u.TS),
		})
	}
	for _, ed := range g.Edges {
		add(g.event[ed.From], g.event[ed.To])
	}
	for _, evs := range byNode {
		for i := 1; i < len(evs); i++ {
			add(evs[i-1], evs[i])
		}
	}
	return out, nil
}
