package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Direction of traversal on a link.
const (
	DirFwd = 0 // From -> To
	DirRev = 1 // To -> From
)

// Capacity sharing modes for a bidirectional link.
const (
	ModeSeparate = "separate" // each direction has its own capacity
	ModeShared   = "shared"   // both directions share one capacity budget
)

type Link struct {
	ID       string  `json:"id"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	Capacity float64 `json:"capacity"` // packets per tick
	Mode     string  `json:"bidirectional"`
}

type Flow struct {
	ID     string   `json:"id"`
	Src    string   `json:"src"`
	Dst    string   `json:"dst"`
	Demand float64  `json:"demand"` // packets per tick
	Path   []string `json:"path"`   // ordered link IDs (fixed route)
}

type FailureScenario struct {
	ID          string   `json:"id"`
	FailedLinks []string `json:"failedLinks"`
}

type SimConfig struct {
	Ticks int `json:"ticks"`
}

type Input struct {
	Name             string            `json:"name"`
	Links            []Link            `json:"links"`
	Flows            []Flow            `json:"flows"`
	FailureScenarios []FailureScenario `json:"failureScenarios"`
	Simulation       *SimConfig        `json:"simulation"`
}

// Hop is one resolved step of a flow route: a link plus traversal direction.
type Hop struct {
	LinkIdx int
	Dir     int
}

// Network is the validated, indexed form of Input.
type Network struct {
	Name      string
	Links     []Link
	Flows     []Flow
	Routes    [][]Hop // Routes[i] is the resolved path of Flows[i]
	Scenarios []FailureScenario
	SimTicks  int

	linkByID map[string]int
}

func LoadInput(path string) (*Input, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &in, nil
}

// BuildNetwork validates the input and resolves every flow route into
// (link, direction) hops. A route that cannot reach its destination is an
// error at build time; routes broken later by a failure scenario are
// reported as unreachable, never as zero-load successes.
func BuildNetwork(in *Input) (*Network, error) {
	n := &Network{
		Name:      in.Name,
		Links:     in.Links,
		Flows:     in.Flows,
		Scenarios: in.FailureScenarios,
		SimTicks:  500,
		linkByID:  map[string]int{},
	}
	if in.Simulation != nil && in.Simulation.Ticks > 0 {
		n.SimTicks = in.Simulation.Ticks
	}
	for i, l := range in.Links {
		if l.ID == "" {
			return nil, fmt.Errorf("link #%d: empty id", i)
		}
		if _, dup := n.linkByID[l.ID]; dup {
			return nil, fmt.Errorf("duplicate link id %q", l.ID)
		}
		if l.From == "" || l.To == "" || l.From == l.To {
			return nil, fmt.Errorf("link %s: bad endpoints %q->%q", l.ID, l.From, l.To)
		}
		if l.Capacity <= 0 {
			return nil, fmt.Errorf("link %s: capacity must be > 0", l.ID)
		}
		if l.Mode != ModeSeparate && l.Mode != ModeShared {
			return nil, fmt.Errorf("link %s: bidirectional must be %q or %q", l.ID, ModeSeparate, ModeShared)
		}
		n.linkByID[l.ID] = i
	}
	seenFlow := map[string]bool{}
	for i, f := range in.Flows {
		if f.ID == "" {
			return nil, fmt.Errorf("flow #%d: empty id", i)
		}
		if seenFlow[f.ID] {
			return nil, fmt.Errorf("duplicate flow id %q", f.ID)
		}
		seenFlow[f.ID] = true
		if f.Demand <= 0 {
			return nil, fmt.Errorf("flow %s: demand must be > 0", f.ID)
		}
		if len(f.Path) == 0 {
			return nil, fmt.Errorf("flow %s: empty path", f.ID)
		}
		route, err := n.resolveRoute(f)
		if err != nil {
			return nil, err
		}
		n.Routes = append(n.Routes, route)
	}
	seenSc := map[string]bool{}
	for _, sc := range in.FailureScenarios {
		if sc.ID == "" {
			return nil, fmt.Errorf("failure scenario with empty id")
		}
		if seenSc[sc.ID] {
			return nil, fmt.Errorf("duplicate failure scenario id %q", sc.ID)
		}
		seenSc[sc.ID] = true
		for _, lid := range sc.FailedLinks {
			if _, ok := n.linkByID[lid]; !ok {
				return nil, fmt.Errorf("scenario %s: unknown link %q", sc.ID, lid)
			}
		}
	}
	return n, nil
}

// resolveRoute walks the declared path link by link, checking that
// consecutive links are adjacent and that the walk goes Src -> Dst.
func (n *Network) resolveRoute(f Flow) ([]Hop, error) {
	cur := f.Src
	route := make([]Hop, 0, len(f.Path))
	for _, lid := range f.Path {
		li, ok := n.linkByID[lid]
		if !ok {
			return nil, fmt.Errorf("flow %s: unknown link %q", f.ID, lid)
		}
		l := n.Links[li]
		var hop Hop
		switch cur {
		case l.From:
			hop = Hop{LinkIdx: li, Dir: DirFwd}
			cur = l.To
		case l.To:
			hop = Hop{LinkIdx: li, Dir: DirRev}
			cur = l.From
		default:
			return nil, fmt.Errorf("flow %s: link %s is not incident to node %q (broken route)", f.ID, lid, cur)
		}
		route = append(route, hop)
	}
	if cur != f.Dst {
		return nil, fmt.Errorf("flow %s: route ends at %q, not dst %q", f.ID, cur, f.Dst)
	}
	return route, nil
}

// LinkIndex returns the index of a link id.
func (n *Network) LinkIndex(id string) (int, bool) {
	i, ok := n.linkByID[id]
	return i, ok
}

// UnreachableFlows returns the indices of flows whose fixed route uses at
// least one of the failed links.
func (n *Network) UnreachableFlows(failed map[int]bool) []int {
	var out []int
	for fi, route := range n.Routes {
		for _, h := range route {
			if failed[h.LinkIdx] {
				out = append(out, fi)
				break
			}
		}
	}
	return out
}

// FailedSet converts a scenario's failed link ids to an index set.
func (n *Network) FailedSet(sc FailureScenario) map[int]bool {
	m := make(map[int]bool, len(sc.FailedLinks))
	for _, lid := range sc.FailedLinks {
		m[n.linkByID[lid]] = true
	}
	return m
}

// sortedIDs is a small helper for deterministic output.
func sortedIDs(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}
