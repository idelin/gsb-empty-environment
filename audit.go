package taskpkg

import (
	"sort"
)

// DependencyPath is one shortest root-to-node route recorded for a finding.
type DependencyPath struct {
	Nodes []NodeID
	Edges []Edge
}

// Finding describes a resolution in which a host-unauthorized candidate was
// selected, together with the smallest platform configuration that exposes it
// and the shortest dependency path that reaches it.
type Finding struct {
	Platform PlatformConfig
	// Popcount is the number of enabled condition bits; the minimal finding
	// minimizes this value first.
	Popcount  int
	Target    NodeID
	Candidate Candidate
	Reason    RejectReason
	Path      DependencyPath
	// Resolution is retained for callers that want the full selected set.
	Resolution *Resolution
}

// Failure records a configuration that could not be resolved at all.
type Failure struct {
	Platform PlatformConfig
	Err      error
}

type bfsStep struct {
	node NodeID
	edge Edge
	from int
}

// Report aggregates an independent enumeration over all configurations.
type Report struct {
	Bits     int
	Findings []Finding
	Failures []Failure
	// Minimal is the finding with the fewest enabled bits, breaking ties by
	// smallest mask and then the shortest dependency path.
	Minimal *Finding
}

// AuditConfig parameterizes an enumeration.
type AuditConfig struct {
	Bits int
}

// Enumerate resolves the graph independently under every one of the 2^Bits
// platform configurations and reports unauthorized selections. Each resolve
// gets a fresh state machine; nothing is shared between configurations.
func (r *Resolver) Enumerate(cfg AuditConfig, policy Policy) (*Report, error) {
	configs, err := Configs(cfg.Bits)
	if err != nil {
		return nil, err
	}
	report := &Report{Bits: cfg.Bits}

	for _, platform := range configs {
		res, err := r.Resolve(Options{Platform: platform})
		if err != nil {
			report.Failures = append(report.Failures, Failure{Platform: platform, Err: err})
			continue
		}
		for _, node := range res.Order {
			cand := res.Selected[node].Candidate
			if ok, _ := policy.Authorized(cand); ok {
				continue
			}
			_, reason := policy.Authorized(cand)
			path, ok := r.shortestPath(platform, node)
			if !ok {
				continue
			}
			report.Findings = append(report.Findings, Finding{
				Platform:   platform,
				Popcount:   popcount(platform),
				Target:     node,
				Candidate:  cand,
				Reason:     reason,
				Path:       path,
				Resolution: res,
			})
		}
	}

	sort.SliceStable(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		if a.Popcount != b.Popcount {
			return a.Popcount < b.Popcount
		}
		if a.Platform != b.Platform {
			return a.Platform < b.Platform
		}
		if len(a.Path.Nodes) != len(b.Path.Nodes) {
			return len(a.Path.Nodes) < len(b.Path.Nodes)
		}
		return a.Target.String() < b.Target.String()
	})
	if len(report.Findings) > 0 {
		minimal := report.Findings[0]
		report.Minimal = &minimal
	}
	return report, nil
}

// shortestPath performs a BFS over active edges to find the shortest route
// from the root to target. Edges are traversed in the deterministic ordering
// produced by Graph.activeEdges, so equal-length paths stay stable.
func (r *Resolver) shortestPath(platform PlatformConfig, target NodeID) (DependencyPath, bool) {
	root := r.Graph.Root
	if root == target {
		return DependencyPath{Nodes: []NodeID{root}}, true
	}
	visited := map[NodeID]bool{root: true}
	queue := []bfsStep{{node: root, from: -1}}
	tail := 0
	for tail < len(queue) {
		cur := queue[tail]
		tail++
		for _, edge := range r.Graph.activeEdges(cur.node, platform) {
			next := edge.To
			if visited[next] {
				continue
			}
			visited[next] = true
			queue = append(queue, bfsStep{node: next, edge: edge, from: tail - 1})
			if next == target {
				return buildPath(queue), true
			}
		}
	}
	return DependencyPath{}, false
}

func buildPath(queue []bfsStep) DependencyPath {
	var nodes []NodeID
	var edges []Edge
	for i := len(queue) - 1; i >= 0; i = queue[i].from {
		nodes = append(nodes, queue[i].node)
		if queue[i].from >= 0 {
			edges = append(edges, queue[i].edge)
		}
	}
	for l, rr := 0, len(nodes)-1; l < rr; l, rr = l+1, rr-1 {
		nodes[l], nodes[rr] = nodes[rr], nodes[l]
	}
	for l, rr := 0, len(edges)-1; l < rr; l, rr = l+1, rr-1 {
		edges[l], edges[rr] = edges[rr], edges[l]
	}
	return DependencyPath{Nodes: nodes, Edges: edges}
}

func popcount(p PlatformConfig) int {
	count := 0
	for i := 0; i < MaxPlatformConditions; i++ {
		if p.Bit(i) {
			count++
		}
	}
	return count
}
