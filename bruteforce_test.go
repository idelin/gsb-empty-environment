package netsec

import (
	"math/rand"
	"sort"
	"testing"
)

type bruteGraph struct {
	root  string
	nodes map[string]Node
	out   map[string][]Edge
}

func compileBruteGraph(g Graph) bruteGraph {
	bg := bruteGraph{
		root:  g.Root,
		nodes: make(map[string]Node, len(g.Nodes)),
		out:   make(map[string][]Edge),
	}
	for _, node := range g.Nodes {
		bg.nodes[node.ID] = node
	}
	for _, edge := range g.Edges {
		bg.out[edge.From] = append(bg.out[edge.From], edge)
	}
	return bg
}

func (g bruteGraph) active(cfg Config) map[string]bool {
	active := map[string]bool{g.root: true}
	queue := []string{g.root}
	for head := 0; head < len(queue); head++ {
		from := queue[head]
		for _, edge := range g.out[from] {
			if active[edge.To] || !edge.When.Eval(cfg) {
				continue
			}
			active[edge.To] = true
			queue = append(queue, edge.To)
		}
	}
	return active
}

func (g bruteGraph) capabilities(cfg Config) map[string]struct{} {
	caps := make(map[string]struct{})
	for node, active := range g.active(cfg) {
		if !active {
			continue
		}
		for _, capability := range g.nodes[node].Capabilities {
			caps[capability] = struct{}{}
		}
	}
	return caps
}

func (g bruteGraph) shortestPathLength(cfg Config, capability string) (int, bool) {
	type queueEntry struct {
		node string
		dist int
	}
	visited := map[string]bool{g.root: true}
	queue := []queueEntry{{node: g.root, dist: 0}}
	for head := 0; head < len(queue); head++ {
		entry := queue[head]
		for _, provided := range g.nodes[entry.node].Capabilities {
			if provided == capability {
				return entry.dist, true
			}
		}
		for _, edge := range g.out[entry.node] {
			if visited[edge.To] || !edge.When.Eval(cfg) {
				continue
			}
			visited[edge.To] = true
			queue = append(queue, queueEntry{node: edge.To, dist: entry.dist + 1})
		}
	}
	return 0, false
}

// bruteForceAdded is deliberately independent of Compare's internal
// reachability machinery: it is a direct full configuration enumeration.
func bruteForceAdded(oldGraph, newGraph Graph) map[string]int {
	variableSet := make(map[string]struct{})
	for _, graph := range []Graph{oldGraph, newGraph} {
		for _, name := range graph.Platforms {
			variableSet[name] = struct{}{}
		}
		for _, edge := range graph.Edges {
			for _, name := range edge.When.Variables() {
				variableSet[name] = struct{}{}
			}
		}
	}
	variables := make([]string, 0, len(variableSet))
	for name := range variableSet {
		variables = append(variables, name)
	}
	sort.Strings(variables)

	oldCompiled := compileBruteGraph(oldGraph)
	newCompiled := compileBruteGraph(newGraph)
	minimumLengths := make(map[string]int)

	for mask := 0; mask < 1<<uint(len(variables)); mask++ {
		cfg := make(Config, len(variables))
		for bit, name := range variables {
			cfg[name] = mask&(1<<uint(bit)) != 0
		}
		oldCaps := oldCompiled.capabilities(cfg)
		newCaps := newCompiled.capabilities(cfg)
		for capability := range newCaps {
			if _, existed := oldCaps[capability]; existed {
				continue
			}
			length, reachable := newCompiled.shortestPathLength(cfg, capability)
			if !reachable {
				continue
			}
			if old, found := minimumLengths[capability]; !found || length < old {
				minimumLengths[capability] = length
			}
		}
	}
	return minimumLengths
}

func TestCompareMatchesFullEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	platforms := []string{"os", "arch", "tag"}
	nodeIDs := []string{"root", "a", "b", "c", "d", "e", "f", "g", "h"}
	capabilities := []string{"postinstall", "preinstall", "build-script", "network-hook"}

	for iteration := 0; iteration < 120; iteration++ {
		oldGraph := randomGraph(rng, nodeIDs, platforms, capabilities)
		newGraph := randomGraph(rng, nodeIDs, platforms, capabilities)

		report, err := Compare(oldGraph, newGraph)
		if err != nil {
			t.Fatalf("iteration %d: Compare: %v", iteration, err)
		}
		want := bruteForceAdded(oldGraph, newGraph)
		got := make(map[string]int, len(report.Added))
		for _, addition := range report.Added {
			if _, duplicate := got[addition.Capability]; duplicate {
				t.Fatalf("iteration %d: duplicate finding for %q", iteration, addition.Capability)
			}
			got[addition.Capability] = len(addition.Path) - 1
			checkFinding(t, iteration, oldGraph, newGraph, addition)
		}
		if len(got) != len(want) {
			t.Fatalf("iteration %d: got %v, want %v", iteration, got, want)
		}
		for capability, length := range want {
			gotLength, ok := got[capability]
			if !ok {
				t.Fatalf("iteration %d: missing capability %q", iteration, capability)
			}
			if gotLength != length {
				t.Fatalf("iteration %d: %q path length = %d, minimum is %d", iteration, capability, gotLength, length)
			}
		}
	}
}

func checkFinding(t *testing.T, iteration int, oldGraph, newGraph Graph, addition AddedCapability) {
	t.Helper()
	oldCompiled := compileBruteGraph(oldGraph)
	newCompiled := compileBruteGraph(newGraph)

	oldCaps := oldCompiled.capabilities(addition.Config)
	if _, existed := oldCaps[addition.Capability]; existed {
		t.Fatalf("iteration %d: %q was already active under %s", iteration, addition.Capability, addition.Config)
	}
	newCaps := newCompiled.capabilities(addition.Config)
	if _, active := newCaps[addition.Capability]; !active {
		t.Fatalf("iteration %d: %q is not active under witness %s", iteration, addition.Capability, addition.Config)
	}
	if len(addition.Path) == 0 || addition.Path[0] != newGraph.Root {
		t.Fatalf("iteration %d: path %#v does not start at root %q", iteration, addition.Path, newGraph.Root)
	}
	if last := addition.Path[len(addition.Path)-1]; last != addition.Node {
		t.Fatalf("iteration %d: path ends at %q, Node is %q", iteration, last, addition.Node)
	}
	hasCapability := false
	for _, capability := range newCompiled.nodes[addition.Node].Capabilities {
		if capability == addition.Capability {
			hasCapability = true
		}
	}
	if !hasCapability {
		t.Fatalf("iteration %d: node %q does not provide %q", iteration, addition.Node, addition.Capability)
	}
	for i := 0; i+1 < len(addition.Path); i++ {
		from, to := addition.Path[i], addition.Path[i+1]
		edgeActive := false
		for _, edge := range newCompiled.out[from] {
			if edge.To == to && edge.When.Eval(addition.Config) {
				edgeActive = true
				break
			}
		}
		if !edgeActive {
			t.Fatalf("iteration %d: path edge %q -> %q is inactive under %s", iteration, from, to, addition.Config)
		}
	}
}

func randomGraph(rng *rand.Rand, nodeIDs, platforms, capabilities []string) Graph {
	g := Graph{Root: nodeIDs[0], Platforms: append([]string(nil), platforms...)}
	for i, id := range nodeIDs {
		node := Node{ID: id}
		if i > 0 && rng.Intn(3) == 0 {
			node.Capabilities = []string{capabilities[rng.Intn(len(capabilities))]}
		}
		g.Nodes = append(g.Nodes, node)
	}
	for from := 0; from < len(nodeIDs); from++ {
		for to := 0; to < len(nodeIDs); to++ {
			if from == to || rng.Intn(7) != 0 {
				continue
			}
			g.Edges = append(g.Edges, Edge{
				From: nodeIDs[from],
				To:   nodeIDs[to],
				When: randomCondition(rng, platforms),
			})
		}
	}
	return g
}

func randomCondition(rng *rand.Rand, platforms []string) Condition {
	a := platforms[rng.Intn(len(platforms))]
	b := platforms[rng.Intn(len(platforms))]
	switch rng.Intn(8) {
	case 0:
		return Always()
	case 1:
		return Flag(a)
	case 2:
		return NotFlag(a)
	case 3:
		return And(Flag(a), Flag(b))
	case 4:
		return Or(Flag(a), NotFlag(b))
	case 5:
		return And(Flag(a), NotFlag(b))
	case 6:
		return Or(Flag(a), Flag(b))
	default:
		return Never()
	}
}
