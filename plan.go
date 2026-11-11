// Package netsec plans safe, stepwise updates for an in-memory network model.
//
// A network is described by at most MaxEdges mutable, undirected edges. Each
// edge is either enabled (its endpoints are connected) or disabled. A request
// supplies the initial and goal switch states together with forbidden reach
// pairs: while a pair is listed, its two nodes must never be in the same
// connected component.
//
// A move flips exactly one edge. Plan runs an exhaustive, full-state BFS over
// the switch-state hypercube, expanding only states whose induced graph
// satisfies every forbidden-pair constraint. Because every edge is mutable,
// BFS returns the minimum number of flips, and when several shortest plans
// exist it returns the one whose edge-ID sequence is lexicographically
// smallest (edges are sorted by ID and flipped in that order).
//
// When no safe migration exists, the result carries evidence: every safe
// state the BFS reached, every unsafe state it blocked, and the discovery
// tree linking them. No helper here performs input, output, or any network
// side effect; all operations apply to transient in-memory copies.
package netsec

import (
	"errors"
	"sort"
)

// MaxEdges is the largest number of mutable edges a request may contain.
const MaxEdges = 12

var (
	// ErrInvalidRequest reports a malformed request: too many edges, duplicate
	// or missing edge IDs, unknown nodes, or an empty edge list.
	ErrInvalidRequest = errors.New("netsec: invalid planning request")
	// ErrInvalidState reports a state map whose keys do not match the request
	// edges exactly.
	ErrInvalidState = errors.New("netsec: state does not match request edges")
)

// Edge is one mutable, undirected link identified by EdgeID.
type Edge struct {
	EdgeID string
	From   string
	To     string
}

// ForbiddenPair declares that From and To must never be mutually reachable.
type ForbiddenPair struct {
	From string
	To   string
}

// Request is the input to Plan.
type Request struct {
	Edges          []Edge
	Initial, Goal  map[string]bool
	ForbiddenReach []ForbiddenPair
}

// Step is a single edge flip in a migration plan.
type Step struct {
	EdgeID  string
	Enabled bool
}

// StateRecord is one observed switch state with its BFS discovery metadata.
type StateRecord struct {
	State       map[string]bool
	Depth       int
	ParentState map[string]bool
	FlippedEdge string
}

// Evidence is the full-state BFS audit trail.
type Evidence struct {
	VisitedSafe []StateRecord
	Blocked     []StateRecord
	TotalStates int
}

// Result is the outcome of Plan.
type Result struct {
	Feasible bool
	Steps    []Step
	Initial  map[string]bool
	Goal     map[string]bool
	Evidence Evidence
	Reason   string
}

type compiled struct {
	ids     []string
	index   map[string]int
	from    []int
	to      []int
	pairs   [][2]int
	nodeIDs map[string]int
}

// validate checks the request and returns edges ordered by ascending ID.
func validate(req *Request) (*compiled, uint16, uint16, error) {
	if req == nil {
		return nil, 0, 0, ErrInvalidRequest
	}
	n := len(req.Edges)
	if n == 0 || n > MaxEdges {
		return nil, 0, 0, ErrInvalidRequest
	}

	ordered := make([]Edge, n)
	copy(ordered, req.Edges)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].EdgeID < ordered[j].EdgeID })

	index := make(map[string]int, n)
	for i, edge := range ordered {
		if edge.EdgeID == "" || edge.From == "" || edge.To == "" {
			return nil, 0, 0, ErrInvalidRequest
		}
		if _, seen := index[edge.EdgeID]; seen {
			return nil, 0, 0, ErrInvalidRequest
		}
		index[edge.EdgeID] = i
	}

	nodeIDs := make(map[string]int)
	nodeOf := func(name string) int {
		id, ok := nodeIDs[name]
		if !ok {
			id = len(nodeIDs)
			nodeIDs[name] = id
		}
		return id
	}
	from := make([]int, n)
	to := make([]int, n)
	for i, edge := range ordered {
		from[i] = nodeOf(edge.From)
		to[i] = nodeOf(edge.To)
	}

	pairs := make([][2]int, 0, len(req.ForbiddenReach))
	for _, pair := range req.ForbiddenReach {
		a, okA := nodeIDs[pair.From]
		b, okB := nodeIDs[pair.To]
		if !okA || !okB {
			return nil, 0, 0, ErrInvalidRequest
		}
		pairs = append(pairs, [2]int{a, b})
	}

	start, err := stateToMask(req.Initial, ordered)
	if err != nil {
		return nil, 0, 0, err
	}
	goal, err := stateToMask(req.Goal, ordered)
	if err != nil {
		return nil, 0, 0, err
	}

	return &compiled{
		ids:     idsOf(ordered),
		index:   index,
		from:    from,
		to:      to,
		pairs:   pairs,
		nodeIDs: nodeIDs,
	}, start, goal, nil
}

func idsOf(ordered []Edge) []string {
	ids := make([]string, len(ordered))
	for i, edge := range ordered {
		ids[i] = edge.EdgeID
	}
	return ids
}

func stateToMask(state map[string]bool, ordered []Edge) (uint16, error) {
	if len(state) != len(ordered) {
		return 0, ErrInvalidState
	}
	var mask uint16
	for i, edge := range ordered {
		value, ok := state[edge.EdgeID]
		if !ok {
			return 0, ErrInvalidState
		}
		if value {
			mask |= 1 << i
		}
	}
	return mask, nil
}

func (c *compiled) maskToState(mask uint16) map[string]bool {
	state := make(map[string]bool, len(c.ids))
	for i, id := range c.ids {
		state[id] = mask&(1<<i) != 0
	}
	return state
}

// safe reports whether the graph induced by mask keeps every forbidden pair in
// separate connected components.
func (c *compiled) safe(mask uint16) bool {
	adjacency := make([][]int, len(c.nodeIDs))
	for i := range c.ids {
		if mask&(1<<i) == 0 {
			continue
		}
		a, b := c.from[i], c.to[i]
		adjacency[a] = append(adjacency[a], b)
		adjacency[b] = append(adjacency[b], a)
	}
	for _, pair := range c.pairs {
		if reachable(adjacency, pair[0], pair[1]) {
			return false
		}
	}
	return true
}

func reachable(adjacency [][]int, source, target int) bool {
	if source == target {
		return true
	}
	seen := make([]bool, len(adjacency))
	seen[source] = true
	queue := []int{source}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[node] {
			if next == target {
				return true
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// Plan computes the shortest safe migration between the initial and goal
// states. It never mutates req and performs no I/O.
func Plan(req *Request) (*Result, error) {
	comp, start, goal, err := validate(req)
	if err != nil {
		return nil, err
	}

	total := 1 << len(comp.ids)
	parent := make([]int, total)
	depth := make([]int, total)
	parentEdge := make([]int, total)
	blockedParent := make([]int, total)
	blockedEdge := make([]int, total)
	blockedSeen := make([]bool, total)
	for i := range parent {
		parent[i] = -1
		parentEdge[i] = -1
		blockedParent[i] = -1
		blockedEdge[i] = -1
	}

	var blockedMasks []int
	var visited []int
	queue := make([]int, 0, total)
	if comp.safe(start) {
		parent[start] = -2
		depth[start] = 0
		queue = append(queue, int(start))
	} else {
		blockedSeen[start] = true
		blockedMasks = append(blockedMasks, int(start))
	}

	// Full-state BFS: edges flip in ascending ID order, so the first discovery
	// of each state yields the lexicographically smallest shortest path.
	for head := 0; head < len(queue); head++ {
		current := queue[head]
		visited = append(visited, current)
		for edgeIndex := range comp.ids {
			next := current ^ (1 << edgeIndex)
			if parent[next] != -1 {
				continue
			}
			if comp.safe(uint16(next)) {
				parent[next] = current
				parentEdge[next] = edgeIndex
				depth[next] = depth[current] + 1
				queue = append(queue, next)
				continue
			}
			blockedParent[next] = current
			blockedEdge[next] = edgeIndex
			if !blockedSeen[next] {
				blockedSeen[next] = true
				blockedMasks = append(blockedMasks, next)
			}
		}
	}

	result := &Result{
		Initial: comp.maskToState(start),
		Goal:    comp.maskToState(goal),
		Evidence: Evidence{
			TotalStates: total,
			VisitedSafe: make([]StateRecord, 0, len(visited)),
			Blocked:     make([]StateRecord, 0, len(blockedMasks)),
		},
	}

	for _, mask := range visited {
		result.Evidence.VisitedSafe = append(result.Evidence.VisitedSafe, comp.record(mask, depth[mask], parent[mask], parentEdge[mask]))
	}
	sort.Ints(blockedMasks)
	for _, mask := range blockedMasks {
		blockedDepth := 0
		if blockedParent[mask] >= 0 {
			blockedDepth = depth[blockedParent[mask]] + 1
		}
		result.Evidence.Blocked = append(result.Evidence.Blocked, comp.record(mask, blockedDepth, blockedParent[mask], blockedEdge[mask]))
	}

	if parent[goal] == -1 {
		result.Feasible = false
		if !comp.safe(start) {
			result.Reason = "initial state violates a forbidden-reach constraint"
		} else if !comp.safe(goal) {
			result.Reason = "goal state violates a forbidden-reach constraint"
		} else {
			result.Reason = "goal state is not reachable without opening a forbidden path"
		}
		return result, nil
	}

	result.Feasible = true
	result.Steps = buildSteps(comp, parent, parentEdge, start, goal)
	return result, nil
}

func (c *compiled) record(mask, depthValue, parentMask, edgeIndex int) StateRecord {
	record := StateRecord{
		State: c.maskToState(uint16(mask)),
		Depth: depthValue,
	}
	if parentMask >= 0 {
		record.ParentState = c.maskToState(uint16(parentMask))
	}
	if edgeIndex >= 0 {
		record.FlippedEdge = c.ids[edgeIndex]
	}
	return record
}

func buildSteps(comp *compiled, parent, parentEdge []int, start, goal uint16) []Step {
	steps := make([]Step, 0)
	for mask := int(goal); mask != int(start); {
		edgeIndex := parentEdge[mask]
		steps = append(steps, Step{
			EdgeID:  comp.ids[edgeIndex],
			Enabled: mask&(1<<edgeIndex) != 0,
		})
		mask = parent[mask]
	}
	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}
	return steps
}
