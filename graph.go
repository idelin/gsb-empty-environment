package netsec

import (
	"fmt"
	"sort"
)

// MaxPlatforms is the maximum number of distinct boolean platform conditions
// accepted by Compare.
const MaxPlatforms = 16

// Node is a dependency-graph node. Capabilities are install-time effects
// provided by that node, such as a lifecycle hook or a build script.
type Node struct {
	ID           string
	Capabilities []string
}

// Edge is a directed dependency edge. When is an optional condition; its zero
// value is true, making the edge unconditional.
type Edge struct {
	From string
	To   string
	When Condition
}

// Graph describes one locked dependency graph. Root must name one of the
// nodes. Platforms may declare platform flags that are not referenced by any
// edge; condition variables referenced by edges are collected automatically.
type Graph struct {
	Root      string
	Platforms []string
	Nodes     []Node
	Edges     []Edge
}

// NewGraph returns an empty graph with the given root.
func NewGraph(root string) *Graph {
	return &Graph{Root: root}
}

// AddNode appends a node.
func (g *Graph) AddNode(id string, capabilities ...string) {
	g.Nodes = append(g.Nodes, Node{ID: id, Capabilities: append([]string(nil), capabilities...)})
}

// AddEdge appends an edge with an optional condition.
func (g *Graph) AddEdge(from, to string, when Condition) {
	g.Edges = append(g.Edges, Edge{From: from, To: to, When: when})
}

// PlatformVariables returns every platform flag declared by Platforms or
// referenced by an edge condition, sorted lexicographically.
func (g Graph) PlatformVariables() ([]string, error) {
	cg, err := compileGraph(g)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), cg.variables...), nil
}

// Validate checks node and edge consistency and the platform-count limit.
func (g Graph) Validate() error {
	cg, err := compileGraph(g)
	if err != nil {
		return err
	}
	if len(cg.variables) > MaxPlatforms {
		return fmt.Errorf("netsec: graph uses %d platform conditions, maximum is %d", len(cg.variables), MaxPlatforms)
	}
	return nil
}

// ActiveNodes returns the node IDs reachable from Root under cfg, sorted
// lexicographically.
func (g Graph) ActiveNodes(cfg Config) ([]string, error) {
	cg, err := compileGraph(g)
	if err != nil {
		return nil, err
	}
	if len(cg.variables) > MaxPlatforms {
		return nil, fmt.Errorf("netsec: graph uses %d platform conditions, maximum is %d", len(cg.variables), MaxPlatforms)
	}
	bitIndex := bitIndexFor(cg.variables)
	result := cg.reach(cg.mask(cfg), bitIndex)
	ids := make([]string, 0, len(cg.ids))
	for i, active := range result.active {
		if active {
			ids = append(ids, cg.ids[i])
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// Reachable is an alias for ActiveNodes.
func (g Graph) Reachable(cfg Config) ([]string, error) {
	return g.ActiveNodes(cfg)
}

// ShortestPath returns a shortest dependency path from Root to target under
// cfg. Among equally short paths it returns the lexicographically smallest one
// and reports false when target is not active.
func (g Graph) ShortestPath(cfg Config, target string) ([]string, bool, error) {
	cg, err := compileGraph(g)
	if err != nil {
		return nil, false, err
	}
	if len(cg.variables) > MaxPlatforms {
		return nil, false, fmt.Errorf("netsec: graph uses %d platform conditions, maximum is %d", len(cg.variables), MaxPlatforms)
	}
	targetIndex, ok := cg.index[target]
	if !ok {
		return nil, false, fmt.Errorf("netsec: unknown node %q", target)
	}
	bitIndex := bitIndexFor(cg.variables)
	result := cg.reach(cg.mask(cfg), bitIndex)
	if !result.active[targetIndex] {
		return nil, false, nil
	}
	return cg.path(result, targetIndex), true, nil
}

type compiledEdge struct {
	to   int
	when Condition
}

type compiledGraph struct {
	ids       []string
	index     map[string]int
	caps      [][]string
	out       [][]compiledEdge
	root      int
	variables []string
}

func compileGraph(g Graph) (*compiledGraph, error) {
	if g.Root == "" {
		return nil, fmt.Errorf("netsec: graph root is empty")
	}

	cg := &compiledGraph{
		ids:   make([]string, 0, len(g.Nodes)),
		index: make(map[string]int, len(g.Nodes)),
		caps:  make([][]string, 0, len(g.Nodes)),
	}
	variableSet := make(map[string]struct{})
	for _, name := range g.Platforms {
		if name == "" {
			return nil, fmt.Errorf("netsec: graph declares an empty platform name")
		}
		variableSet[name] = struct{}{}
	}

	for _, node := range g.Nodes {
		if node.ID == "" {
			return nil, fmt.Errorf("netsec: node ID is empty")
		}
		if _, exists := cg.index[node.ID]; exists {
			return nil, fmt.Errorf("netsec: duplicate node %q", node.ID)
		}
		seenCaps := make(map[string]struct{}, len(node.Capabilities))
		nodeCaps := make([]string, 0, len(node.Capabilities))
		for _, capability := range node.Capabilities {
			if capability == "" {
				return nil, fmt.Errorf("netsec: node %q has an empty capability", node.ID)
			}
			if _, seen := seenCaps[capability]; seen {
				continue
			}
			seenCaps[capability] = struct{}{}
			nodeCaps = append(nodeCaps, capability)
		}
		cg.index[node.ID] = len(cg.ids)
		cg.ids = append(cg.ids, node.ID)
		cg.caps = append(cg.caps, nodeCaps)
	}

	rootIndex, ok := cg.index[g.Root]
	if !ok {
		return nil, fmt.Errorf("netsec: root %q is not a node", g.Root)
	}
	cg.root = rootIndex
	cg.out = make([][]compiledEdge, len(cg.ids))

	for edgeNumber, edge := range g.Edges {
		from, ok := cg.index[edge.From]
		if !ok {
			return nil, fmt.Errorf("netsec: edge %d has unknown source %q", edgeNumber, edge.From)
		}
		to, ok := cg.index[edge.To]
		if !ok {
			return nil, fmt.Errorf("netsec: edge %d has unknown destination %q", edgeNumber, edge.To)
		}
		for _, name := range edge.When.Variables() {
			if name == "" {
				return nil, fmt.Errorf("netsec: edge %d references an empty platform name", edgeNumber)
			}
			variableSet[name] = struct{}{}
		}
		cg.out[from] = append(cg.out[from], compiledEdge{to: to, when: edge.When})
	}

	for from := range cg.out {
		sort.SliceStable(cg.out[from], func(i, j int) bool {
			return cg.ids[cg.out[from][i].to] < cg.ids[cg.out[from][j].to]
		})
	}
	cg.variables = sortedNames(variableSet)
	return cg, nil
}

func (cg *compiledGraph) mask(cfg Config) uint32 {
	var mask uint32
	for bit, name := range cg.variables {
		if cfg[name] {
			mask |= 1 << uint(bit)
		}
	}
	return mask
}

func bitIndexFor(variables []string) map[string]uint {
	index := make(map[string]uint, len(variables))
	for bit, name := range variables {
		index[name] = uint(bit)
	}
	return index
}

type reachResult struct {
	active []bool
	dist   []int
	prev   []int
}

func (cg *compiledGraph) reach(mask uint32, bitIndex map[string]uint) reachResult {
	result := reachResult{
		active: make([]bool, len(cg.ids)),
		dist:   make([]int, len(cg.ids)),
		prev:   make([]int, len(cg.ids)),
	}
	for i := range result.dist {
		result.dist[i] = -1
		result.prev[i] = -1
	}

	queue := make([]int, 0, len(cg.ids))
	result.active[cg.root] = true
	result.dist[cg.root] = 0
	queue = append(queue, cg.root)

	for head := 0; head < len(queue); head++ {
		from := queue[head]
		for _, edge := range cg.out[from] {
			if result.active[edge.to] || !edge.when.evalMask(mask, bitIndex) {
				continue
			}
			result.active[edge.to] = true
			result.dist[edge.to] = result.dist[from] + 1
			result.prev[edge.to] = from
			queue = append(queue, edge.to)
		}
	}
	return result
}

func (cg *compiledGraph) path(result reachResult, target int) []string {
	if target < 0 || target >= len(result.active) || !result.active[target] {
		return nil
	}
	reversed := make([]string, 0, result.dist[target]+1)
	for current := target; current >= 0; current = result.prev[current] {
		reversed = append(reversed, cg.ids[current])
	}
	path := make([]string, len(reversed))
	for i := range reversed {
		path[len(reversed)-1-i] = reversed[i]
	}
	return path
}
