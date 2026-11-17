package netsec

import (
	"fmt"
	"math/bits"
	"sort"
	"strings"
)

// MaxMutableEdges is the largest number of switchable edges accepted by Plan.
// The complete state space is therefore at most 2^12 = 4096 states.
const MaxMutableEdges = 12

// Edge is a switchable network link. The planner may turn it on or off, but
// any single step changes exactly one such edge.
type Edge struct {
	ID             string
	From           string
	To             string
	InitialEnabled bool
	TargetEnabled  bool
}

// FixedEdge is always present in every state.
type FixedEdge struct {
	From string
	To   string
}

// ForbiddenPair identifies a reachability that must not occur in any state,
// including the initial and target states.
type ForbiddenPair struct {
	From string
	To   string
}

// Request describes one in-memory network update. Directed controls whether
// every mutable and fixed edge is traversable in one direction or both.
type Request struct {
	Edges      []Edge
	FixedEdges []FixedEdge
	Forbidden  []ForbiddenPair
	Directed   bool
}

// State stores the on/off bits of the mutable edges. Bit i corresponds to the
// i-th edge after edges have been sorted lexicographically by ID.
type State uint16

// Enabled reports whether the mutable edge at index is enabled.
func (s State) Enabled(index int) bool {
	return s&(State(1)<<uint(index)) != 0
}

// Count returns the number of enabled mutable edges.
func (s State) Count() int {
	return bits.OnesCount16(uint16(s))
}

type normalizedModel struct {
	edges       []Edge
	initial     State
	target      State
	nodes       map[string]int
	baseAdj     [][]int
	forbidden   []ForbiddenPair
	forbiddenAt []forbiddenGroup
	directed    bool
}

type forbiddenGroup struct {
	source int
	pairs  []int
}

func newNormalizedModel(req Request) (*normalizedModel, error) {
	if len(req.Edges) > MaxMutableEdges {
		return nil, fmt.Errorf(
			"netsec: %d mutable edges exceeds the limit of %d",
			len(req.Edges), MaxMutableEdges,
		)
	}

	edges := append([]Edge(nil), req.Edges...)
	ids := make(map[string]struct{}, len(edges))
	for i, edge := range edges {
		if strings.TrimSpace(edge.ID) == "" {
			return nil, fmt.Errorf("netsec: edge[%d] has an empty ID", i)
		}
		if _, exists := ids[edge.ID]; exists {
			return nil, fmt.Errorf("netsec: duplicate edge ID %q", edge.ID)
		}
		ids[edge.ID] = struct{}{}
		if err := validateEndpoint("edge "+edge.ID+".From", edge.From); err != nil {
			return nil, err
		}
		if err := validateEndpoint("edge "+edge.ID+".To", edge.To); err != nil {
			return nil, err
		}
	}

	sort.Slice(edges, func(i, j int) bool {
		return edges[i].ID < edges[j].ID
	})

	for i, edge := range req.FixedEdges {
		if err := validateEndpoint(fmt.Sprintf("fixedEdges[%d].From", i), edge.From); err != nil {
			return nil, err
		}
		if err := validateEndpoint(fmt.Sprintf("fixedEdges[%d].To", i), edge.To); err != nil {
			return nil, err
		}
	}

	for i, pair := range req.Forbidden {
		if err := validateEndpoint(fmt.Sprintf("forbidden[%d].From", i), pair.From); err != nil {
			return nil, err
		}
		if err := validateEndpoint(fmt.Sprintf("forbidden[%d].To", i), pair.To); err != nil {
			return nil, err
		}
		if pair.From == pair.To {
			return nil, fmt.Errorf(
				"netsec: forbidden[%d] must name two different nodes", i,
			)
		}
	}

	nodeIndex := make(map[string]int)
	nextNode := func(name string) int {
		if index, exists := nodeIndex[name]; exists {
			return index
		}
		index := len(nodeIndex)
		nodeIndex[name] = index
		return index
	}

	for _, edge := range req.FixedEdges {
		nextNode(edge.From)
		nextNode(edge.To)
	}
	for _, edge := range edges {
		nextNode(edge.From)
		nextNode(edge.To)
	}
	for _, pair := range req.Forbidden {
		nextNode(pair.From)
		nextNode(pair.To)
	}

	baseAdj := make([][]int, len(nodeIndex))
	for _, edge := range req.FixedEdges {
		from, to := nodeIndex[edge.From], nodeIndex[edge.To]
		baseAdj[from] = append(baseAdj[from], to)
		if !req.Directed {
			baseAdj[to] = append(baseAdj[to], from)
		}
	}

	forbidden := make([]ForbiddenPair, 0, len(req.Forbidden))
	seenForbidden := make(map[ForbiddenPair]struct{}, len(req.Forbidden))
	for _, pair := range req.Forbidden {
		if _, exists := seenForbidden[pair]; exists {
			continue
		}
		seenForbidden[pair] = struct{}{}
		forbidden = append(forbidden, pair)
	}

	groupIndex := make(map[int]int)
	groups := make([]forbiddenGroup, 0)
	for i, pair := range forbidden {
		source := nodeIndex[pair.From]
		group, exists := groupIndex[source]
		if !exists {
			group = len(groups)
			groupIndex[source] = group
			groups = append(groups, forbiddenGroup{source: source})
		}
		groups[group].pairs = append(groups[group].pairs, i)
	}

	model := &normalizedModel{
		edges:       edges,
		nodes:       nodeIndex,
		baseAdj:     baseAdj,
		forbidden:   forbidden,
		forbiddenAt: groups,
		directed:    req.Directed,
	}
	for i, edge := range edges {
		if edge.InitialEnabled {
			model.initial |= State(1) << uint(i)
		}
		if edge.TargetEnabled {
			model.target |= State(1) << uint(i)
		}
	}

	return model, nil
}

func validateEndpoint(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("netsec: %s is empty", field)
	}
	return nil
}

func (m *normalizedModel) stateCount() int {
	return 1 << uint(len(m.edges))
}

func (m *normalizedModel) enabledEdgeIDs(state State) []string {
	ids := make([]string, 0, len(m.edges))
	for i, edge := range m.edges {
		if state.Enabled(i) {
			ids = append(ids, edge.ID)
		}
	}
	return ids
}

func (m *normalizedModel) violations(state State) []ForbiddenPair {
	if len(m.forbidden) == 0 {
		return nil
	}

	adjacency := m.adjacency(state)
	violations := make([]ForbiddenPair, 0)
	for _, group := range m.forbiddenAt {
		reachable := walk(adjacency, group.source)
		for _, pairIndex := range group.pairs {
			pair := m.forbidden[pairIndex]
			to := m.lookupNode(pair.To)
			if reachable[to] {
				violations = append(violations, pair)
			}
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return violations
}

func (m *normalizedModel) lookupNode(name string) int {
	return m.nodes[name]
}

func (m *normalizedModel) adjacency(state State) [][]int {
	adjacency := make([][]int, len(m.baseAdj))
	for from := range m.baseAdj {
		adjacency[from] = append([]int(nil), m.baseAdj[from]...)
	}
	for i, edge := range m.edges {
		if !state.Enabled(i) {
			continue
		}
		from, to := m.lookupNode(edge.From), m.lookupNode(edge.To)
		adjacency[from] = append(adjacency[from], to)
		if !m.directed {
			adjacency[to] = append(adjacency[to], from)
		}
	}
	return adjacency
}

func walk(adjacency [][]int, source int) []bool {
	reachable := make([]bool, len(adjacency))
	reachable[source] = true
	queue := []int{source}
	for head := 0; head < len(queue); head++ {
		from := queue[head]
		for _, to := range adjacency[from] {
			if reachable[to] {
				continue
			}
			reachable[to] = true
			queue = append(queue, to)
		}
	}
	return reachable
}
