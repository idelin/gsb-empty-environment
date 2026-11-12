package handshake

import (
	"fmt"
	"math/bits"
)

// SolveWithEdges finds the minimum-change topological ordering of the given
// messages under explicit precedence edges. It is the generic entry point used
// by Solve and is also suitable when the analysis host derives causality edges
// itself. The length of messages defines the node count; edge endpoints must
// reference message IDs.
func SolveWithEdges(messages []Message, edges []Edge) (*Result, error) {
	n := len(messages)
	if n > MaxMessages {
		return nil, fmt.Errorf("handshake: %d messages exceed maximum of %d", n, MaxMessages)
	}

	index := make(map[string]int, n)
	for i, msg := range messages {
		if msg.ID == "" {
			return nil, fmt.Errorf("handshake: message at index %d has an empty ID", i)
		}
		if _, dup := index[msg.ID]; dup {
			return nil, fmt.Errorf("handshake: duplicate message ID %q", msg.ID)
		}
		index[msg.ID] = i
	}

	// preds[v] is the set of direct predecessors of node v. Duplicate edges
	// are collapsed while the first supplied reason is retained.
	preds := make([]uint, n)
	reason := make(map[[2]int]EdgeReason, len(edges))
	for _, edge := range edges {
		from, okFrom := index[edge.From]
		to, okTo := index[edge.To]
		if !okFrom || !okTo {
			return nil, fmt.Errorf("handshake: edge %q -> %q references an unknown message ID", edge.From, edge.To)
		}
		if from == to {
			return nil, fmt.Errorf("handshake: self-loop on message %q", edge.From)
		}
		key := [2]int{from, to}
		if _, seen := reason[key]; !seen {
			reason[key] = edge.Reason
		}
		preds[to] |= 1 << uint(from)
	}

	if ring, ringEdges := findCycle(messages, preds, reason); ring != nil {
		return nil, &CycleError{Ring: ring, Edges: ringEdges}
	}

	if n == 0 {
		return &Result{Order: []string{}, Swaps: 0}, nil
	}

	// dp[mask] is the minimum inversion count of any topological ordering of
	// the nodes contained in mask. When v is appended after the nodes of prev,
	// every already-placed node observed after v contributes one inversion.
	total := uint(1) << uint(n)
	dp := make([]int, total)
	for mask := uint(1); mask < total; mask++ {
		best := -1
		remaining := mask
		for remaining != 0 {
			bit := remaining & -remaining
			v := bits.TrailingZeros(uint(bit))
			remaining ^= bit
			if preds[v]&^mask != 0 {
				continue
			}
			prev := mask ^ bit
			if dp[prev] < 0 {
				continue
			}
			candidate := dp[prev] + appendCost(prev, v)
			if best < 0 || candidate < best {
				best = candidate
			}
		}
		dp[mask] = best
	}

	// Reconstruct from the front using the exact same append cost as the DP.
	// At each step the eligible node that preserves optimality is chosen by
	// smallest message ID, yielding the lexicographically smallest optimum.
	order := make([]string, 0, n)
	mask := uint(0)
	for len(order) < n {
		chosen := -1
		remaining := (total - 1) &^ mask
		for remaining != 0 {
			bit := remaining & -remaining
			v := bits.TrailingZeros(uint(bit))
			remaining ^= bit
			next := mask | bit
			if preds[v]&^next != 0 {
				continue
			}
			candidate := dp[mask] + appendCost(mask, v)
			if candidate != dp[next] {
				continue
			}
			if chosen < 0 || messages[v].ID < messages[chosen].ID {
				chosen = v
			}
		}
		if chosen < 0 {
			return nil, fmt.Errorf("handshake: internal error: no eligible node while reconstructing order")
		}
		order = append(order, messages[chosen].ID)
		mask |= 1 << uint(chosen)
	}

	return &Result{Order: order, Swaps: dp[total-1]}, nil
}

// appendCost counts nodes already placed in mask whose observed position is
// later than node v's. Appending v after them creates one inversion per such
// node. The expression avoids complementing a mask, which would set the high
// bits of the unsigned word.
func appendCost(mask uint, v int) int {
	placed := bits.OnesCount(mask)
	before := 0
	if v > 0 {
		before = bits.OnesCount(mask & ((uint(1) << uint(v)) - 1))
	}
	return placed - before
}

// findCycle runs an iterative DFS coloring walk. It returns the first directed
// cycle as a closed ring (first node repeated last) together with the edges
// traversed on that ring. A nil ring means the precedence graph is acyclic.
func findCycle(messages []Message, preds []uint, reason map[[2]int]EdgeReason) ([]string, []Edge) {
	n := len(messages)
	succs := make([][]int, n)
	for to := 0; to < n; to++ {
		predBits := preds[to]
		for predBits != 0 {
			bit := predBits & -predBits
			from := bits.TrailingZeros(uint(bit))
			predBits ^= bit
			succs[from] = append(succs[from], to)
		}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]uint8, n)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = -1
	}

	for start := 0; start < n; start++ {
		if color[start] != white {
			continue
		}
		type frame struct {
			node int
			next int
		}
		stack := []frame{{node: start, next: 0}}
		color[start] = gray

		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.next >= len(succs[top.node]) {
				color[top.node] = black
				stack = stack[:len(stack)-1]
				continue
			}
			child := succs[top.node][top.next]
			top.next++
			switch color[child] {
			case white:
				color[child] = gray
				parent[child] = top.node
				stack = append(stack, frame{node: child, next: 0})
			case gray:
				// Walk parent pointers from the current node back to child,
				// then reverse so the ring reads child -> ... -> node -> child.
				path := []int{top.node}
				for path[len(path)-1] != child {
					path = append(path, parent[path[len(path)-1]])
				}
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				path = append(path, child)

				ring := make([]string, len(path))
				for i, node := range path {
					ring[i] = messages[node].ID
				}
				ringEdges := make([]Edge, 0, len(path)-1)
				for i := 0; i+1 < len(path); i++ {
					ringEdges = append(ringEdges, Edge{
						From:   ring[i],
						To:     ring[i+1],
						Reason: reason[[2]int{path[i], path[i+1]}],
					})
				}
				return ring, ringEdges
			}
		}
	}
	return nil, nil
}
