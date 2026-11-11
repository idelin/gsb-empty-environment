package netsec

import "fmt"

// node is one event plus the analytic state derived for it.
type node struct {
	evt      Event
	window   Interval
	feasible bool

	pred []int // trusted strict-predecessor indices (smaller seq, same feed)
	succ []int
}

// constraintGraph groups the events of one credential and turns the trusted
// commit sequences into strict-ordering edges (t_pred + 1 <= t_succ).
// Events on different feeds stay unconnected, because no trusted ordering
// exists across feeds even when their intervals overlap.
type constraintGraph struct {
	nodes []*node
}

// buildConstraintGraph constructs the per-credential DAG. Duplicate seqs on
// one feed and ordering cycles are reported as conflicts rather than dropping
// either event.
func buildConstraintGraph(events []Event) (*constraintGraph, []Conflict, error) {
	g := &constraintGraph{nodes: make([]*node, 0, len(events))}
	for _, evt := range events {
		g.nodes = append(g.nodes, &node{
			evt:      evt,
			window:   evt.Window(),
			feasible: true,
		})
	}

	var conflicts []Conflict

	// Within each feed, order events by seq and link adjacent ones.
	feedEvents := make(map[string][]int)
	for i, n := range g.nodes {
		feedEvents[n.evt.Feed] = append(feedEvents[n.evt.Feed], i)
	}
	for feed, ids := range feedEvents {
		sortNodesBySeq(g.nodes, ids)
		lastSeq := int64(0)
		for k, i := range ids {
			cur := g.nodes[i]
			if k > 0 && cur.evt.Seq == lastSeq {
				conflicts = append(conflicts, Conflict{
					Kind:    ConflictDuplicateSeq,
					EventID: cur.evt.ID,
					Detail: fmt.Sprintf("feed %q already carries seq %d",
						feed, cur.evt.Seq),
				})
				// The trust relation between equal-seq events is undefined;
				// keep both but do not synthesize an edge.
				continue
			}
			if k > 0 {
				pred := g.nodes[ids[k-1]]
				pred.succ = append(pred.succ, i)
				cur.pred = append(cur.pred, ids[k-1])
			}
			lastSeq = cur.evt.Seq
		}
	}

	order, ok := g.topoOrder()
	if !ok {
		// Same-feed links are always low-seq -> high-seq, so a cycle means
		// the input contradicts the trusted ordering contract.
		return g, conflicts, fmt.Errorf("netsec: ordering cycle among %d events", len(g.nodes))
	}

	g.propagate(order, &conflicts)
	return g, conflicts, nil
}

// topoOrder returns a topological ordering via Kahn's algorithm.
func (g *constraintGraph) topoOrder() ([]int, bool) {
	indeg := make([]int, len(g.nodes))
	for i, n := range g.nodes {
		indeg[i] = len(n.pred)
	}
	queue := make([]int, 0, len(g.nodes))
	for i, d := range indeg {
		if d == 0 {
			queue = append(queue, i)
		}
	}
	order := make([]int, 0, len(g.nodes))
	for head := 0; head < len(queue); head++ {
		i := queue[head]
		order = append(order, i)
		for _, j := range g.nodes[i].succ {
			indeg[j]--
			if indeg[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	return order, len(order) == len(g.nodes)
}

// propagate narrows every window using the difference constraints.
//
// Forward pass enforces t_pred + 1 <= t_succ on lower bounds; the backward
// pass enforces t_succ - 1 >= t_pred on upper bounds. Each pass walks a DAG
// once, so path constraints accumulate exactly and a single sweep per
// direction reaches the fixed point.
func (g *constraintGraph) propagate(order []int, conflicts *[]Conflict) {
	for _, i := range order {
		n := g.nodes[i]
		for _, p := range n.pred {
			if c := inc(g.nodes[p].window.Lo); c > n.window.Lo {
				n.window.Lo = c
			}
		}
		if n.window.Empty() {
			n.feasible = false
			*conflicts = append(*conflicts, Conflict{
				Kind:    ConflictInconsistentWindow,
				EventID: n.evt.ID,
				Detail: fmt.Sprintf("window collapses to [%d,%d] under sequence constraints",
					n.window.Lo, n.window.Hi),
			})
		}
	}

	for k := len(order) - 1; k >= 0; k-- {
		n := g.nodes[order[k]]
		for _, s := range n.succ {
			if c := dec(g.nodes[s].window.Hi); c < n.window.Hi {
				n.window.Hi = c
			}
		}
		if n.window.Empty() {
			if n.feasible {
				n.feasible = false
				*conflicts = append(*conflicts, Conflict{
					Kind:    ConflictInconsistentWindow,
					EventID: n.evt.ID,
					Detail: fmt.Sprintf("window collapses to [%d,%d] under sequence constraints",
						n.window.Lo, n.window.Hi),
				})
			}
		}
	}
}

// canReach reports whether a path from a to b exists in the DAG. A path means
// the trusted commit sequence forces event a strictly before event b.
func (g *constraintGraph) canReach(a, b int) bool {
	if a == b {
		return false
	}
	seen := make([]bool, len(g.nodes))
	stack := append([]int(nil), g.nodes[a].succ...)
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if i == b {
			return true
		}
		if seen[i] {
			continue
		}
		seen[i] = true
		stack = append(stack, g.nodes[i].succ...)
	}
	return false
}

func sortNodesBySeq(nodes []*node, ids []int) {
	// Insertion sort: per-feed event counts are small, and a stable order
	// keeps deterministic tie handling.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && nodes[ids[j]].evt.Seq < nodes[ids[j-1]].evt.Seq; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
