package main

import (
	"fmt"
	"strings"
)

// WaitEdge is one "waits on" relation: node From holds Packet, which
// cannot advance because a resource owned by node To is full.
type WaitEdge struct {
	From   int
	To     int
	Packet int
	Reason string
}

// WaitForEdges builds the wait-for graph for a given state:
//   - a buffered packet whose outgoing edge queue is full waits on the
//     edge's destination node;
//   - the head packet of an edge queue whose receiver buffer is full
//     waits on that receiver.
func WaitForEdges(cfg Config, edgeIdx [][]int, buffers, queues [][]int, pkts []Packet) []WaitEdge {
	var out []WaitEdge
	for n := 0; n < cfg.Nodes; n++ {
		for _, pid := range buffers[n] {
			p := pkts[pid]
			if p.Dst == n {
				continue
			}
			nxt := cfg.NextHop[n][p.Dst]
			ei := edgeIdx[n][nxt]
			if ei >= 0 && len(queues[ei]) >= cfg.Edges[ei].Cap {
				out = append(out, WaitEdge{
					From:   n,
					To:     nxt,
					Packet: pid,
					Reason: fmt.Sprintf("edge %d->%d queue full (%d/%d)", n, nxt, len(queues[ei]), cfg.Edges[ei].Cap),
				})
			}
		}
	}
	for ei, e := range cfg.Edges {
		if len(queues[ei]) == 0 {
			continue
		}
		if len(buffers[e.To]) >= cfg.BufferCap {
			out = append(out, WaitEdge{
				From:   e.From,
				To:     e.To,
				Packet: queues[ei][0],
				Reason: fmt.Sprintf("node %d buffer full (%d/%d)", e.To, len(buffers[e.To]), cfg.BufferCap),
			})
		}
	}
	return out
}

// FindWaitCycle returns a wait-for cycle proving deadlock, or nil.
func FindWaitCycle(cfg Config, edgeIdx [][]int, buffers, queues [][]int, pkts []Packet) []WaitEdge {
	edges := WaitForEdges(cfg, edgeIdx, buffers, queues, pkts)
	adj := make(map[int][]WaitEdge)
	for _, we := range edges {
		adj[we.From] = append(adj[we.From], we)
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[int]int)
	var stack []WaitEdge
	var cycle []WaitEdge
	var dfs func(n int) bool
	dfs = func(n int) bool {
		color[n] = gray
		for _, we := range adj[n] {
			if cycle != nil {
				return true
			}
			switch color[we.To] {
			case gray:
				// Found a back edge: extract the cycle from the stack.
				start := 0
				for i, se := range stack {
					if se.From == we.To {
						start = i
						break
					}
				}
				cycle = append(append([]WaitEdge(nil), stack[start:]...), we)
				return true
			case white:
				stack = append(stack, we)
				if dfs(we.To) {
					return true
				}
				stack = stack[:len(stack)-1]
			}
		}
		color[n] = black
		return false
	}
	for n := 0; n < cfg.Nodes; n++ {
		if cycle != nil {
			break
		}
		if color[n] == white {
			dfs(n)
		}
	}
	return cycle
}

// FormatCycle renders a wait-for cycle as human-readable evidence.
func FormatCycle(cycle []WaitEdge) string {
	if len(cycle) == 0 {
		return "(no wait-for cycle found)"
	}
	var b strings.Builder
	b.WriteString("wait-for cycle:\n")
	for _, we := range cycle {
		fmt.Fprintf(&b, "  node %d --pkt %d--> node %d   [%s]\n", we.From, we.Packet, we.To, we.Reason)
	}
	fmt.Fprintf(&b, "  (node %d waits on node %d, closing the cycle)\n",
		cycle[len(cycle)-1].From, cycle[len(cycle)-1].To)
	return b.String()
}
