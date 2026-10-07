// Package traj rebuilds logistics trajectories from directed edges.
//
// A trajectory is an Eulerian trail over a directed multigraph: every
// edge is used exactly once. Duplicate legs between the same pair of
// sites are distinguished by their edge IDs. Among all valid trails
// from the required start point, the library selects the one whose
// edge-ID sequence is lexicographically smallest.
package traj

import (
	"fmt"
	"sort"
)

// Edge is one directed leg of a trajectory. IDs must be unique within a
// graph; From and To are site identifiers.
type Edge struct {
	ID   string
	From string
	To   string
}

// FailureKind classifies why no Eulerian trail exists.
type FailureKind int

const (
	// FailDegree means the in/out degree balance forbids a trail
	// starting at the required point.
	FailDegree FailureKind = iota + 1
	// FailConnectivity means the edges split into components that
	// cannot all be reached from the start point.
	FailConnectivity
)

func (k FailureKind) String() string {
	switch k {
	case FailDegree:
		return "degree"
	case FailConnectivity:
		return "connectivity"
	}
	return "unknown"
}

// Diagnosis describes why a route cannot be built. It is nil when the
// edge set admits a valid trail.
type Diagnosis struct {
	Kind    FailureKind
	Message string
	// Balance holds out-degree minus in-degree for every imbalanced
	// site (degree failures only).
	Balance map[string]int
	// Unreachable lists sites holding edges that cannot be reached
	// from the start point (connectivity failures only).
	Unreachable []string
}

func (d *Diagnosis) Error() string {
	if d == nil {
		return "no failure"
	}
	return fmt.Sprintf("%s failure: %s", d.Kind, d.Message)
}

// checkEuler reports whether edges admit an Eulerian trail starting at
// start. A nil result means feasible; otherwise the Diagnosis explains
// the degree or connectivity failure.
func checkEuler(edges []Edge, start string) *Diagnosis {
	if len(edges) == 0 {
		return nil
	}
	balance := make(map[string]int)
	adj := make(map[string][]string)
	verts := make(map[string]bool)
	for _, e := range edges {
		balance[e.From]++
		balance[e.To]--
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
		verts[e.From] = true
		verts[e.To] = true
	}

	// Degree condition: every site balanced, except optionally the
	// start (out-in = 1) and one end site (out-in = -1).
	plus, minus := "", ""
	imbalanced := make(map[string]int)
	bad := false
	keys := make([]string, 0, len(balance))
	for v := range balance {
		keys = append(keys, v)
	}
	sort.Strings(keys)
	for _, v := range keys {
		switch b := balance[v]; {
		case b == 0:
		case b == 1 && plus == "":
			plus = v
			imbalanced[v] = b
		case b == -1 && minus == "":
			minus = v
			imbalanced[v] = b
		default:
			imbalanced[v] = b
			bad = true
		}
	}
	if bad || plus != minus && (plus == "" || minus == "") {
		return &Diagnosis{
			Kind:    FailDegree,
			Message: fmt.Sprintf("imbalanced sites %v", imbalanced),
			Balance: imbalanced,
		}
	}
	if plus != "" && plus != start {
		return &Diagnosis{
			Kind:    FailDegree,
			Message: fmt.Sprintf("trail must begin at %s (out-in=1), not %s", plus, start),
			Balance: imbalanced,
		}
	}

	// Connectivity: every site holding an edge must be reachable from
	// start in the underlying undirected graph.
	if !verts[start] {
		unreached := make([]string, 0, len(verts))
		for v := range verts {
			unreached = append(unreached, v)
		}
		sort.Strings(unreached)
		return &Diagnosis{
			Kind:        FailConnectivity,
			Message:     fmt.Sprintf("start %s holds no edge", start),
			Unreachable: unreached,
		}
	}
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range adj[v] {
			if !seen[w] {
				seen[w] = true
				queue = append(queue, w)
			}
		}
	}
	var unreached []string
	for v := range verts {
		if !seen[v] {
			unreached = append(unreached, v)
		}
	}
	if len(unreached) > 0 {
		sort.Strings(unreached)
		return &Diagnosis{
			Kind:        FailConnectivity,
			Message:     fmt.Sprintf("sites unreachable from %s: %v", start, unreached),
			Unreachable: unreached,
		}
	}
	return nil
}
