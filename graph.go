package taskpkg

import (
	"errors"
	"fmt"
)

// SignedEdge is undirected polarity evidence. Sign +1 means the channels have
// the same polarity; -1 means their polarities are opposite.
type SignedEdge struct {
	A           int     `json:"a"`
	B           int     `json:"b"`
	Sign        int     `json:"sign"`
	Correlation float64 `json:"correlation,omitempty"`
	EnergyRatio float64 `json:"energy_ratio,omitempty"`
	Consecutive int     `json:"consecutive,omitempty"`
	FirstWindow int     `json:"first_window"`
	LastWindow  int     `json:"last_window"`
}

// CycleStep is one directed traversal in a conflict cycle. Edge identifies the
// input signed edge used by the step.
type CycleStep struct {
	From      int `json:"from"`
	To        int `json:"to"`
	Sign      int `json:"sign"`
	EdgeIndex int `json:"edge_index"`
}

// ConflictCycle is a shortest cycle whose signed product is -1. Nodes contains
// each vertex once; the closing traversal is represented by the last step.
type ConflictCycle struct {
	Nodes  []int       `json:"nodes"`
	Steps  []CycleStep `json:"steps"`
	Length int         `json:"length"`
}

// PolarityResult reports whether a globally consistent polarity assignment
// exists. Assignment values are +1 and -1; the global sign is arbitrary.
type PolarityResult struct {
	Consistent bool           `json:"consistent"`
	Assignment []int          `json:"assignment,omitempty"`
	Conflict   *ConflictCycle `json:"conflict,omitempty"`
}

// CheckPolarity determines whether the signed evidence admits a consistent
// polarity assignment and, if not, returns a shortest contradiction cycle.
func CheckPolarity(channelCount int, edges []SignedEdge) (PolarityResult, error) {
	if channelCount < 0 {
		return PolarityResult{}, errors.New("taskpkg: negative channel count")
	}
	g, err := newGraph(channelCount, edges)
	if err != nil {
		return PolarityResult{}, err
	}

	assignment := make([]int, channelCount)
	consistent := true
	for start := 0; start < channelCount; start++ {
		if assignment[start] != 0 {
			continue
		}
		assignment[start] = 1
		queue := []int{start}
		for head := 0; head < len(queue); head++ {
			u := queue[head]
			for _, adjacent := range g.adj[u] {
				want := assignment[u] * adjacent.sign
				if assignment[adjacent.to] == 0 {
					assignment[adjacent.to] = want
					queue = append(queue, adjacent.to)
					continue
				}
				if assignment[adjacent.to] != want {
					consistent = false
				}
			}
		}
	}
	if consistent {
		return PolarityResult{Consistent: true, Assignment: assignment}, nil
	}
	return PolarityResult{
		Consistent: false,
		Conflict:   shortestConflictCycle(g),
	}, nil
}

type adjacency struct {
	to   int
	sign int
	edge int
}

type signedGraph struct {
	n     int
	edges []SignedEdge
	adj   [][]adjacency
}

func newGraph(n int, edges []SignedEdge) (*signedGraph, error) {
	g := &signedGraph{n: n, edges: make([]SignedEdge, len(edges)), adj: make([][]adjacency, n)}
	for i, edge := range edges {
		if edge.A < 0 || edge.A >= n || edge.B < 0 || edge.B >= n {
			return nil, fmt.Errorf("taskpkg: edge %d has channel index outside [0, %d)", i, n)
		}
		if edge.Sign != 1 && edge.Sign != -1 {
			return nil, fmt.Errorf("taskpkg: edge %d sign must be +1 or -1", i)
		}
		g.edges[i] = edge
		g.adj[edge.A] = append(g.adj[edge.A], adjacency{to: edge.B, sign: edge.Sign, edge: i})
		if edge.A != edge.B {
			g.adj[edge.B] = append(g.adj[edge.B], adjacency{to: edge.A, sign: edge.Sign, edge: i})
		}
	}
	return g, nil
}

func stateIndex(node, parity int) int {
	if parity > 0 {
		return 2 * node
	}
	return 2*node + 1
}

func stateParts(index int) (node, parity int) {
	if index%2 == 0 {
		return index / 2, 1
	}
	return index / 2, -1
}

// shortestConflictCycle checks every edge as the closing edge and searches for
// a shortest path whose parity makes the resulting cycle negative.
func shortestConflictCycle(g *signedGraph) *ConflictCycle {
	var best *ConflictCycle
	for excluded := range g.edges {
		edge := g.edges[excluded]
		if edge.A == edge.B {
			if edge.Sign == -1 {
				return &ConflictCycle{
					Nodes:  []int{edge.A},
					Steps:  []CycleStep{{From: edge.A, To: edge.A, Sign: -1, EdgeIndex: excluded}},
					Length: 1,
				}
			}
			continue
		}
		cycle := shortestParityPathCycle(g, excluded, edge.A, edge.B, -edge.Sign)
		if cycle == nil {
			continue
		}
		if best == nil || cycle.Length < best.Length {
			best = cycle
		}
	}
	return best
}

func shortestParityPathCycle(g *signedGraph, excluded, start, goal, wantedParity int) *ConflictCycle {
	stateCount := 2 * g.n
	previous := make([]int, stateCount)
	previousEdge := make([]int, stateCount)
	for i := range previous {
		previous[i] = -2
		previousEdge[i] = -1
	}
	startState := stateIndex(start, 1)
	previous[startState] = -1
	queue := make([]int, 0, stateCount)
	queue = append(queue, startState)
	targetState := stateIndex(goal, wantedParity)

	for head := 0; head < len(queue) && previous[targetState] == -2; head++ {
		state := queue[head]
		node, parity := stateParts(state)
		for _, next := range g.adj[node] {
			if next.edge == excluded {
				continue
			}
			nextParity := parity * next.sign
			nextState := stateIndex(next.to, nextParity)
			if previous[nextState] != -2 {
				continue
			}
			previous[nextState] = state
			previousEdge[nextState] = next.edge
			queue = append(queue, nextState)
		}
	}
	if previous[targetState] == -2 {
		return nil
	}

	var pathEdges []int
	for state := targetState; state != startState; {
		pathEdges = append(pathEdges, previousEdge[state])
		state = previous[state]
	}
	for left, right := 0, len(pathEdges)-1; left < right; left, right = left+1, right-1 {
		pathEdges[left], pathEdges[right] = pathEdges[right], pathEdges[left]
	}

	// Reconstruct the node order from the path states. Random-edge traversal
	// during reconstruction is avoided by following each edge's endpoint.
	nodes := make([]int, 0, len(pathEdges)+1)
	steps := make([]CycleStep, 0, len(pathEdges)+1)
	nodes = append(nodes, start)
	current := start
	for _, edgeIndex := range pathEdges {
		edge := g.edges[edgeIndex]
		next := edge.B
		if edge.B == current {
			next = edge.A
		}
		if current != edge.A && current != edge.B {
			// This should be impossible because the BFS was built from these
			// same edges, but keep the reconstruction defensive.
			return nil
		}
		if edge.A == edge.B {
			next = edge.A
		}
		steps = append(steps, CycleStep{From: current, To: next, Sign: edge.Sign, EdgeIndex: edgeIndex})
		current = next
		nodes = append(nodes, current)
	}
	// Nodes ends at goal; the closing edge returns to start and is represented
	// only by the final step, so no duplicate start node is stored.
	steps = append(steps, CycleStep{
		From: goal, To: start, Sign: g.edges[excluded].Sign, EdgeIndex: excluded,
	})
	cycle := simplifyNegativeCycle(nodes, steps)
	if cycle == nil {
		return nil
	}
	return cycle
}

// simplifyNegativeCycle removes repeated-vertex closed walks. A negative
// closed walk decomposes into a negative subcycle and its complement, so
// recursively removing a repeated vertex cannot increase the cycle length.
func simplifyNegativeCycle(nodes []int, steps []CycleStep) *ConflictCycle {
	if len(nodes) == 0 || len(steps) == 0 || len(nodes) != len(steps) {
		return nil
	}
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			if nodes[i] != nodes[j] {
				continue
			}
			segmentSign := 1
			for k := i; k < j; k++ {
				segmentSign *= steps[k].Sign
			}
			if segmentSign < 0 {
				segmentNodes := append([]int(nil), nodes[i:j]...)
				segmentSteps := append([]CycleStep(nil), steps[i:j]...)
				if simplified := simplifyNegativeCycle(segmentNodes, segmentSteps); simplified != nil {
					return simplified
				}
			}
			// The segment has positive parity. Splice it out and keep the
			// remainder, whose parity is still negative.
			remainingNodes := make([]int, 0, len(nodes)-(j-i))
			remainingNodes = append(remainingNodes, nodes[:i+1]...)
			remainingNodes = append(remainingNodes, nodes[j+1:]...)
			remainingSteps := make([]CycleStep, 0, len(steps)-(j-i))
			remainingSteps = append(remainingSteps, steps[:i]...)
			remainingSteps = append(remainingSteps, steps[j:]...)
			if len(remainingNodes) > 0 {
				return simplifyNegativeCycle(remainingNodes, remainingSteps)
			}
		}
	}
	cycle := &ConflictCycle{
		Nodes:  append([]int(nil), nodes...),
		Steps:  append([]CycleStep(nil), steps...),
		Length: len(steps),
	}
	return cycle
}

// ShortestConflictCycle is a convenience wrapper around CheckPolarity.
func ShortestConflictCycle(channelCount int, edges []SignedEdge) (*ConflictCycle, error) {
	result, err := CheckPolarity(channelCount, edges)
	if err != nil {
		return nil, err
	}
	return result.Conflict, nil
}
