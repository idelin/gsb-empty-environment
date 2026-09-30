package main

import (
	"sort"
	"strings"
)

// LinkLoad aggregates the demand of all active flows traversing one link in
// one direction. Shared links additionally evaluate both directions summed.
type LinkLoad struct {
	LinkID  string
	Dir     int
	FlowIDs []string
	Load    float64
}

// LinkVerdict is the capacity check result for one link.
type LinkVerdict struct {
	LinkID   string
	Mode     string
	Capacity float64
	Dirs     [2]LinkLoad
	// For ModeShared the binding constraint is Dirs[0].Load + Dirs[1].Load.
	CombinedLoad float64
	Overloaded   bool
	// MinimalSets holds inclusion-minimal flow subsets whose combined demand
	// exceeds the applicable capacity (empty when not overloaded).
	MinimalSets [][]string
	Truncated   bool
}

// FlowRedundancy reports whether a flow has a link-disjoint backup path.
type FlowRedundancy struct {
	FlowID    string
	Redundant bool
	AltPath   []string // link IDs of one alternate path, if found
}

// ScenarioReport is the full verification result for one failure scenario
// (or the baseline, when Failed is empty).
type ScenarioReport struct {
	ScenarioID  string
	FailedLinks []string
	Unreachable []string // flow IDs broken by the failure: never counted as OK
	Verdicts    []LinkVerdict
	AnyOverload bool
}

const (
	maxEnumFlowsPerLink = 20  // above this, switch to greedy approximation
	maxMinimalSets      = 128 // cap on enumerated minimal sets
)

// activeFlows returns indices of flows not in the unreachable set.
func activeFlows(n *Network, unreachable map[int]bool) []int {
	var out []int
	for i := range n.Flows {
		if !unreachable[i] {
			out = append(out, i)
		}
	}
	return out
}

// computeLoads accumulates, for every link and direction, the demands of all
// active flows routed over it.
func computeLoads(n *Network, active []int) [][2]LinkLoad {
	loads := make([][2]LinkLoad, len(n.Links))
	for li, l := range n.Links {
		for d := 0; d < 2; d++ {
			loads[li][d] = LinkLoad{LinkID: l.ID, Dir: d}
		}
	}
	for _, fi := range active {
		f := n.Flows[fi]
		for _, h := range n.Routes[fi] {
			ll := &loads[h.LinkIdx][h.Dir]
			ll.Load += f.Demand
			ll.FlowIDs = append(ll.FlowIDs, f.ID)
		}
	}
	return loads
}

// evaluateLink checks one link's loads against its capacity and, when
// overloaded, computes the minimal relevant flow sets.
func evaluateLink(l Link, loads [2]LinkLoad) LinkVerdict {
	v := LinkVerdict{
		LinkID:   l.ID,
		Mode:     l.Mode,
		Capacity: l.Capacity,
		Dirs:     loads,
	}
	if l.Mode == ModeShared {
		v.CombinedLoad = loads[0].Load + loads[1].Load
		v.Overloaded = v.CombinedLoad > l.Capacity
		if v.Overloaded {
			ids := append(append([]string{}, loads[0].FlowIDs...), loads[1].FlowIDs...)
			v.MinimalSets, v.Truncated = minimalOverloadSets(ids, demandOf, l.Capacity)
		}
		return v
	}
	for d := 0; d < 2; d++ {
		if loads[d].Load > l.Capacity {
			v.Overloaded = true
			sets, trunc := minimalOverloadSets(loads[d].FlowIDs, demandOf, l.Capacity)
			v.MinimalSets = append(v.MinimalSets, sets...)
			v.Truncated = v.Truncated || trunc
		}
	}
	return v
}

// demandOf is rebound per analysis run via package-level lookup.
var demandOf = func(id string) float64 { return 0 }

func withDemandLookup(n *Network, fn func()) {
	lookup := make(map[string]float64, len(n.Flows))
	for _, f := range n.Flows {
		lookup[f.ID] = f.Demand
	}
	prev := demandOf
	demandOf = func(id string) float64 { return lookup[id] }
	defer func() { demandOf = prev }()
	fn()
}

// minimalOverloadSets enumerates inclusion-minimal subsets of flowIDs whose
// summed demand strictly exceeds capacity: removing any single flow from a
// minimal set brings the sum back to <= capacity. When the flow count is too
// large for exact enumeration, a greedy minimal set is returned instead and
// truncated is true.
func minimalOverloadSets(flowIDs []string, demand func(string) float64, capacity float64) ([][]string, bool) {
	ids := sortedIDs(flowIDs)
	if len(ids) == 0 {
		return nil, false
	}
	if len(ids) > maxEnumFlowsPerLink {
		return [][]string{greedyMinimalSet(ids, demand, capacity)}, true
	}
	var sets [][]string
	truncated := false
	// Order flows by descending demand so big contributors are fixed first;
	// enumeration itself remains exact over subsets.
	sorted := append([]string(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool {
		if demand(sorted[i]) != demand(sorted[j]) {
			return demand(sorted[i]) > demand(sorted[j])
		}
		return sorted[i] < sorted[j]
	})
	var dfs func(start int, chosen []string, sum float64)
	dfs = func(start int, chosen []string, sum float64) {
		if truncated {
			return
		}
		if sum > capacity {
			if isMinimal(chosen, demand, capacity) {
				if len(sets) >= maxMinimalSets {
					truncated = true
					return
				}
				sets = append(sets, append([]string(nil), chosen...))
			}
			return // supersets are never minimal
		}
		for i := start; i < len(sorted); i++ {
			dfs(i+1, append(chosen, sorted[i]), sum+demand(sorted[i]))
		}
	}
	dfs(0, nil, 0)
	// Deduplicate (same set may be reached once only, but keep it safe).
	seen := map[string]bool{}
	out := sets[:0]
	for _, s := range sets {
		key := strings.Join(s, ",")
		if !seen[key] {
			seen[key] = true
			out = append(out, s)
		}
	}
	return out, truncated
}

// isMinimal reports whether chosen sums above capacity but every single-flow
// removal drops back to <= capacity.
func isMinimal(chosen []string, demand func(string) float64, capacity float64) bool {
	sum := 0.0
	for _, id := range chosen {
		sum += demand(id)
	}
	if sum <= capacity {
		return false
	}
	for _, id := range chosen {
		if sum-demand(id) > capacity {
			return false
		}
	}
	return true
}

// greedyMinimalSet builds one inclusion-minimal overloading subset in
// O(k log k) for links with too many flows to enumerate exactly.
func greedyMinimalSet(ids []string, demand func(string) float64, capacity float64) []string {
	sorted := append([]string(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool {
		if demand(sorted[i]) != demand(sorted[j]) {
			return demand(sorted[i]) > demand(sorted[j])
		}
		return sorted[i] < sorted[j]
	})
	var chosen []string
	sum := 0.0
	for _, id := range sorted {
		if sum > capacity {
			break
		}
		chosen = append(chosen, id)
		sum += demand(id)
	}
	// Drop any flow whose removal keeps the sum above capacity.
	for i := 0; i < len(chosen); {
		if sum-demand(chosen[i]) > capacity {
			sum -= demand(chosen[i])
			chosen = append(chosen[:i], chosen[i+1:]...)
			continue
		}
		i++
	}
	sort.Strings(chosen)
	return chosen
}

// analyzeScenario runs the static capacity verification for one failure set
// (empty set = baseline).
func analyzeScenario(n *Network, scenarioID string, failed map[int]bool) ScenarioReport {
	rep := ScenarioReport{ScenarioID: scenarioID}
	for li := range failed {
		rep.FailedLinks = append(rep.FailedLinks, n.Links[li].ID)
	}
	rep.FailedLinks = sortedIDs(rep.FailedLinks)

	unreach := map[int]bool{}
	for _, fi := range n.UnreachableFlows(failed) {
		unreach[fi] = true
		rep.Unreachable = append(rep.Unreachable, n.Flows[fi].ID)
	}
	rep.Unreachable = sortedIDs(rep.Unreachable)

	active := activeFlows(n, unreach)
	loads := computeLoads(n, active)
	withDemandLookup(n, func() {
		for li, l := range n.Links {
			if failed[li] {
				continue
			}
			v := evaluateLink(l, loads[li])
			if v.Overloaded {
				rep.AnyOverload = true
			}
			rep.Verdicts = append(rep.Verdicts, v)
		}
	})
	return rep
}

// checkRedundancy verifies, per flow, whether the full topology offers a
// backup path that is link-disjoint from the flow's fixed route.
func checkRedundancy(n *Network) []FlowRedundancy {
	out := make([]FlowRedundancy, 0, len(n.Flows))
	for fi, f := range n.Flows {
		blocked := map[int]bool{}
		for _, h := range n.Routes[fi] {
			blocked[h.LinkIdx] = true
		}
		alt := findPath(n, f.Src, f.Dst, blocked)
		fr := FlowRedundancy{FlowID: f.ID, Redundant: alt != nil}
		if alt != nil {
			for _, li := range alt {
				fr.AltPath = append(fr.AltPath, n.Links[li].ID)
			}
		}
		out = append(out, fr)
	}
	return out
}

// findPath BFS over the node graph avoiding blocked links. Returns link
// indices of one path, or nil.
func findPath(n *Network, src, dst string, blocked map[int]bool) []int {
	if src == dst {
		return []int{}
	}
	adj := map[string][]int{}
	for li, l := range n.Links {
		if blocked[li] {
			continue
		}
		adj[l.From] = append(adj[l.From], li)
		adj[l.To] = append(adj[l.To], li)
	}
	type item struct {
		node string
		via  int // link used to arrive
	}
	prev := map[string]item{src: {node: "", via: -1}}
	queue := []string{src}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == dst {
			var path []int
			for node := dst; prev[node].via != -1; node = prev[node].node {
				path = append([]int{prev[node].via}, path...)
			}
			return path
		}
		for _, li := range adj[cur] {
			l := n.Links[li]
			next := l.To
			if next == cur {
				next = l.From
			}
			if _, seen := prev[next]; !seen {
				prev[next] = item{node: cur, via: li}
				queue = append(queue, next)
			}
		}
	}
	return nil
}

// formatSet renders a minimal flow set compactly.
func formatSet(s []string) string {
	return "{" + strings.Join(s, ", ") + "}"
}
