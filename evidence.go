package netsim

import (
	"fmt"
	"sort"
	"strings"
)

// Hash returns a canonical string key for a state (used by enumeration
// and by the node-order-independence check).
func (s *Sim) Hash(st *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "t%d|", st.Tick)
	for _, q := range st.Queues {
		fmt.Fprintf(&b, "%v;", q)
	}
	b.WriteString("|")
	for _, q := range st.Inject {
		fmt.Fprintf(&b, "%v;", q)
	}
	b.WriteString("|")
	ids := make([]int, 0, len(st.Delivered))
	for id := range st.Delivered {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "%d@%d,", id, st.Delivered[id])
	}
	return b.String()
}

// Describe renders a human-readable snapshot of a state.
func (s *Sim) Describe(st *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tick %d\n", st.Tick)
	for _, e := range s.Net.Edges {
		fmt.Fprintf(&b, "  edge %d (%d->%d, cap %d): [", e.ID, e.From, e.To, e.Capacity)
		for i, pid := range st.Queues[e.ID] {
			if i > 0 {
				b.WriteString(", ")
			}
			p := s.packet(pid)
			fmt.Fprintf(&b, "p%d(dst %d)", p.ID, p.Dst)
		}
		b.WriteString("]\n")
	}
	for v := 0; v < s.Net.NumNodes; v++ {
		if len(st.Inject[v]) > 0 {
			fmt.Fprintf(&b, "  node %d inject queue: %v\n", v, st.Inject[v])
		}
	}
	if len(st.Delivered) > 0 {
		ids := make([]int, 0, len(st.Delivered))
		for id := range st.Delivered {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		fmt.Fprintf(&b, "  delivered: %v\n", ids)
	}
	return b.String()
}

// CheckInvariants verifies packet conservation (no loss, no duplication)
// and edge capacity limits. It returns a list of violations (empty = ok).
func (s *Sim) CheckInvariants(st *State) []string {
	var errs []string
	seen := map[int]string{}
	mark := func(pid int, where string) {
		if prev, dup := seen[pid]; dup {
			errs = append(errs, fmt.Sprintf("packet %d duplicated: %s and %s", pid, prev, where))
		}
		seen[pid] = where
	}
	for _, e := range s.Net.Edges {
		if len(st.Queues[e.ID]) > e.Capacity {
			errs = append(errs, fmt.Sprintf("edge %d over capacity: %d > %d", e.ID, len(st.Queues[e.ID]), e.Capacity))
		}
		for _, pid := range st.Queues[e.ID] {
			mark(pid, fmt.Sprintf("edge %d", e.ID))
		}
	}
	for v, q := range st.Inject {
		for _, pid := range q {
			mark(pid, fmt.Sprintf("inject queue of node %d", v))
		}
	}
	for pid := range st.Delivered {
		mark(pid, "delivered")
	}
	if len(seen) != len(s.Packets) {
		for _, p := range s.Packets {
			if _, ok := seen[p.ID]; !ok {
				errs = append(errs, fmt.Sprintf("packet %d lost", p.ID))
			}
		}
	}
	return errs
}

// waitEdge is one arc of the wait-for graph: a packet held at node From
// cannot proceed because the edge toward node To is full.
type waitEdge struct {
	From, To int
	Packet   int
	Edge     *Edge // the full edge being waited on
}

// waitForGraph derives the wait-for graph of a stuck state: for every
// head-of-queue (or head-of-injection) packet whose next edge is full,
// an arc holder -> owner-of-full-edge.
func (s *Sim) waitForGraph(st *State) []waitEdge {
	var ws []waitEdge
	add := func(holder int, p *Packet) {
		if f := s.Net.NextEdge(holder, p.Dst); f != nil && s.room(st, f) == 0 {
			ws = append(ws, waitEdge{From: holder, To: f.To, Packet: p.ID, Edge: f})
		}
	}
	for _, e := range s.Net.Edges {
		if q := st.Queues[e.ID]; len(q) > 0 {
			if p := s.packet(q[0]); p.Dst != e.To {
				add(e.To, p)
			}
		}
	}
	for v := 0; v < s.Net.NumNodes; v++ {
		if q := st.Inject[v]; len(q) > 0 {
			if p := s.packet(q[0]); p.Dst != v && p.InjectTick <= st.Tick {
				add(v, p)
			}
		}
	}
	return ws
}

// findCycle returns one cycle of the wait-for graph, or nil.
func findCycle(ws []waitEdge) []waitEdge {
	adj := map[int][]waitEdge{}
	for _, w := range ws {
		adj[w.From] = append(adj[w.From], w)
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[int]int{}
	var stack []waitEdge
	var cycle []waitEdge
	var dfs func(v int) bool
	dfs = func(v int) bool {
		color[v] = gray
		for _, w := range adj[v] {
			if color[w.To] == gray {
				start := 0
				for i, sw := range stack {
					if sw.From == w.From {
						start = i
						break
					}
				}
				cycle = append(append([]waitEdge{}, stack[start:]...), w)
				return true
			}
			if color[w.To] == white {
				stack = append(stack, w)
				if dfs(w.To) {
					return true
				}
				stack = stack[:len(stack)-1]
			}
		}
		color[v] = black
		return false
	}
	for _, w := range ws {
		if color[w.From] == white && dfs(w.From) {
			return cycle
		}
	}
	return nil
}

// DeadlockEvidence explains why a state is deadlocked: the full snapshot,
// every blocked head packet, and a circular-wait cycle if one exists.
func (s *Sim) DeadlockEvidence(st *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "deadlock at tick %d: %d of %d packets undelivered\n",
		st.Tick, len(s.Packets)-len(st.Delivered), len(s.Packets))
	b.WriteString(s.Describe(st))
	ws := s.waitForGraph(st)
	for _, w := range ws {
		p := s.packet(w.Packet)
		fmt.Fprintf(&b, "blocked: p%d (dst %d) held at node %d, edge %d->%d (cap %d) full\n",
			p.ID, p.Dst, w.From, w.Edge.From, w.Edge.To, w.Edge.Capacity)
	}
	if cyc := findCycle(ws); cyc != nil {
		b.WriteString("wait-for cycle:\n")
		for _, w := range cyc {
			p := s.packet(w.Packet)
			fmt.Fprintf(&b, "  node %d holds p%d (dst %d), waits for edge %d->%d (full)\n",
				w.From, p.ID, p.Dst, w.Edge.From, w.Edge.To)
		}
		b.WriteString("  cycle: ")
		for i, w := range cyc {
			if i > 0 {
				b.WriteString(" -> ")
			}
			fmt.Fprintf(&b, "%d", w.From)
		}
		fmt.Fprintf(&b, " -> %d\n", cyc[0].From)
	}
	return b.String()
}
