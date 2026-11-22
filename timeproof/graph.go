package timeproof

import "sort"

// NodeLabel uniquely identifies a difference-constraint variable. The origin
// is the true-time zero; "query" is the queried instant; send/recv nodes are
// the local monotonic event instants of a receipt.
type NodeLabel struct {
	Kind    string // "origin", "query", "send", "recv"
	Receipt int    // receipt index; -1 for origin and query
}

// Edge is one difference constraint x_To - x_From <= Weight.
type Edge struct {
	From       NodeLabel
	To         NodeLabel
	Weight     *Rat
	Provenance string
}

func originNode() NodeLabel { return NodeLabel{Kind: "origin", Receipt: -1} }
func queryNode() NodeLabel  { return NodeLabel{Kind: "query", Receipt: -1} }
func sendNode(i int) NodeLabel {
	return NodeLabel{Kind: "send", Receipt: i}
}
func recvNode(i int) NodeLabel {
	return NodeLabel{Kind: "recv", Receipt: i}
}

type point struct {
	label NodeLabel
	mono  *Rat
}

// buildGraph constructs the difference-constraint system for one choice of
// kept receipts. kept[i] is true when receipt i is retained.
func buildGraph(q *Query, kept []bool) ([]NodeLabel, []Edge) {
	origin := originNode()
	query := queryNode()

	points := []point{{label: query, mono: ratCopy(q.QueryMono)}}
	for i, r := range q.Receipts {
		if !kept[i] {
			continue
		}
		points = append(points, point{label: sendNode(i), mono: ratCopy(r.SendMono)})
		points = append(points, point{label: recvNode(i), mono: ratCopy(r.RecvMono)})
	}

	// Stable ordering by monotonic value, preserving insertion order on ties.
	idx := make(map[NodeLabel]int)
	for i, p := range points {
		idx[p.label] = i
	}
	sort.SliceStable(points, func(a, b int) bool {
		return points[a].mono.Cmp(points[b].mono) < 0
	})

	nodes := make([]NodeLabel, 0, len(points)+1)
	nodes = append(nodes, origin)
	for _, p := range points {
		nodes = append(nodes, p.label)
	}

	var edges []Edge

	// Piecewise drift chain over adjacent monotonic points.
	for k := 0; k+1 < len(points); k++ {
		a, b := points[k], points[k+1]
		lo, hi := q.Clock.elapsed(a.mono, b.mono)
		// x_b - x_a <= hi
		edges = append(edges, Edge{a.label, b.label, ratCopy(hi), "drift"})
		// x_a - x_b <= -lo
		edges = append(edges, Edge{b.label, a.label, negRat(lo), "drift"})
	}

	// Receipt-derived constraints.
	for i, r := range q.Receipts {
		if !kept[i] {
			continue
		}
		s, rv := sendNode(i), recvNode(i)

		var dlo *Rat = RatInt(0)
		var dhi *Rat // nil means unbounded
		if r.OneWayDelay != nil {
			dlo = ratCopy(r.OneWayDelay)
			dhi = ratCopy(r.OneWayDelay)
		}

		// T_send in [L - dhi, H - dlo]
		sendLo := subRat(r.SignedLo, dhi)
		sendHi := subRat(r.SignedHi, dlo)
		// T_recv in [L + dlo, H + phi + dhi]
		recvLo := addRat(r.SignedLo, dlo)
		recvHi := addRat(r.SignedHi, addRat(r.ProcessHi, dhi))
		// round trip in [2 dlo + plo, 2 dhi + phi]
		rttLo := addRat(addRat(dlo, dlo), r.ProcessLo)
		rttHi := addRat(addRat(dhi, dhi), r.ProcessHi)

		addBound := func(node NodeLabel, lo, hi *Rat) {
			if hi != nil { // x_node - x_origin <= hi
				edges = append(edges, Edge{origin, node, ratCopy(hi), "signed-upper"})
			}
			if lo != nil { // x_origin - x_node <= -lo
				edges = append(edges, Edge{node, origin, negRat(lo), "signed-lower"})
			}
		}
		addBound(s, sendLo, sendHi)
		addBound(rv, recvLo, recvHi)

		if rttHi != nil { // x_recv - x_send <= rttHi
			edges = append(edges, Edge{s, rv, ratCopy(rttHi), "rtt-upper"})
		}
		if rttLo != nil { // x_send - x_recv <= -rttLo
			edges = append(edges, Edge{rv, s, negRat(rttLo), "rtt-lower"})
		}
	}

	return nodes, edges
}

func addRat(a, b *Rat) *Rat {
	if a == nil || b == nil {
		return nil
	}
	return new(Rat).Add(a, b)
}

func subRat(a, b *Rat) *Rat {
	if a == nil || b == nil {
		return nil
	}
	return new(Rat).Sub(a, b)
}

// bfResult holds a Bellman-Ford run outcome.
type bfResult struct {
	feasible bool
	dist     []*Rat // nil entry means +infinity (unreachable)
	pred     []int  // predecessor edge index, -1 if none
	cycle    []Edge // negative cycle when infeasible
}

// bellmanFord runs Bellman-Ford from src. It returns the shortest paths and,
// on infeasibility, one extracted negative cycle.
func bellmanFord(nodes []NodeLabel, edges []Edge, src int) bfResult {
	n := len(nodes)
	index := make(map[NodeLabel]int, n)
	for i, l := range nodes {
		index[l] = i
	}
	dist := make([]*Rat, n)
	pred := make([]int, n)
	for i := range pred {
		pred[i] = -1
	}
	dist[src] = RatInt(0)

	relax := func() bool {
		changed := false
		for ei, e := range edges {
			u := index[e.From]
			v := index[e.To]
			if dist[u] == nil {
				continue
			}
			nd := new(Rat).Add(dist[u], e.Weight)
			if dist[v] == nil || nd.Cmp(dist[v]) < 0 {
				dist[v] = nd
				pred[v] = ei
				changed = true
			}
		}
		return changed
	}

	for i := 0; i < n-1; i++ {
		if !relax() {
			break
		}
	}
	// One additional convergence pass stabilises predecessor pointers into a
	// shortest-path tree (negative-weight edges can otherwise leave transient
	// predecessor cycles). If anything still relaxes, a negative cycle exists.
	if relax() {
		var badEdge int = -1
		for ei, e := range edges {
			u := index[e.From]
			v := index[e.To]
			if dist[u] == nil {
				continue
			}
			if nd := new(Rat).Add(dist[u], e.Weight); dist[v] == nil || nd.Cmp(dist[v]) < 0 {
				badEdge = ei
				break
			}
		}
		cycle := extractCycle(nodes, edges, index, pred, badEdge)
		return bfResult{feasible: false, dist: dist, pred: pred, cycle: cycle}
	}
	return bfResult{feasible: true, dist: dist, pred: pred}
}

// extractCycle walks predecessor edges from the endpoint of a still-relaxable
// edge until a node repeats, returning the repeated node's cycle.
func extractCycle(nodes []NodeLabel, edges []Edge, index map[NodeLabel]int, pred []int, badEdge int) []Edge {
	n := len(nodes)
	// Walk n+1 predecessor steps from the relaxable edge's endpoint. A
	// negative cycle guarantees a repeated node within these steps.
	x := index[edges[badEdge].To]
	for i := 0; i < n; i++ {
		if pred[x] == -1 {
			return []Edge{edges[badEdge]}
		}
		x = index[edges[pred[x]].From]
	}
	// x is now a node on the cycle. Walk one full lap and collect edges.
	var collected []Edge
	cur := x
	for {
		ei := pred[cur]
		if ei == -1 {
			return []Edge{edges[badEdge]}
		}
		collected = append(collected, edges[ei])
		cur = index[edges[ei].From]
		if cur == x {
			break
		}
		if len(collected) > n {
			return []Edge{edges[badEdge]}
		}
	}

	// The predecessor walk visits cycle edges, but not always in traversal
	// order (a node can enter the cycle through a chord). Reorder by chaining
	// edges end-to-end so the result is a single closed walk.
	byFrom := map[NodeLabel]Edge{}
	for _, e := range collected {
		byFrom[e.From] = e
	}
	out := make([]Edge, 0, len(collected))
	startNode := collected[0].From
	node := startNode
	for {
		e, ok := byFrom[node]
		if !ok {
			return []Edge{edges[badEdge]}
		}
		out = append(out, e)
		node = e.To
		if node == startNode {
			break
		}
		if len(out) > len(collected) {
			return []Edge{edges[badEdge]}
		}
	}
	return out
}

// reverseEdges returns the reversed constraint graph (each edge flipped).
func reverseEdges(edges []Edge) []Edge {
	out := make([]Edge, len(edges))
	for i, e := range edges {
		out[i] = Edge{From: e.To, To: e.From, Weight: ratCopy(e.Weight), Provenance: e.Provenance}
	}
	return out
}
