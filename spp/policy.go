package spp

import (
	"fmt"
	"sort"
)

// LocalPrefTier is the strategy-level local preference applied to a path.
// Higher tiers are strictly preferred. Within one tier paths are ordered by
// deterministic policy tie breakers (AS-path length, lexicographic).
type LocalPrefTier int

// Standard Gao-Rexford preference tiers: customer-learned routes beat
// peer-learned routes, which beat provider-learned routes.
const (
	PrefProvider LocalPrefTier = 0
	PrefPeer     LocalPrefTier = 1
	PrefCustomer LocalPrefTier = 2
)

// PathRank is one fully ordered, loop-free permitted path for an AS.
type PathRank struct {
	Path       []int
	Rank       int // 0 = most preferred; strict total order.
	Tier       LocalPrefTier
	ValleyFree bool
	// Explicit marks paths supplied via ExtraPaths. Explicit paths preserve
	// their supplied strict order instead of being re-sorted by local-pref.
	Explicit bool
}

// PolicyConfig configures how candidate paths of every AS are expanded and
// ordered for a single destination prefix.
type PolicyConfig struct {
	// Origin is the AS originating the destination prefix d.
	Origin int
	// StrictGR rejects valley paths and runs export-filter simulation.
	StrictGR bool
	// TierFor overrides the preference tier of a specific path of a specific
	// AS (keyed by rendered path). Overrides model backup links /
	// cross-carrier local-pref engineering that breaks monotonicity.
	TierFor map[int]map[string]LocalPrefTier
	// ExtraPaths explicitly admits abstract (possibly non-valley-free)
	// paths; used by the classic Bad Gadget / Disagree gadgets. Each entry
	// lists paths from most to least preferred.
	ExtraPaths map[int][][]int
	// MaxPathLength bounds simple-path expansion (defaults to node count).
	MaxPathLength int
	// ExplicitOnly disables automatic simple-path expansion and keeps only
	// ExtraPaths (plus the origin route). It is required by abstract SPP
	// gadgets such as Bad Gadget and DISAGREE, whose permitted-path sets are
	// given explicitly rather than derived from full topology enumeration.
	ExplicitOnly bool
}

// explicitOrder records the caller-supplied strict order of explicit paths.
type explicitOrder map[string]int

// Violation describes one concrete Gao-Rexford or loop-prevention violation.
type Violation struct {
	Kind   string // "valley", "illegal-export", "commercial-cycle", "loop", "origin"
	AS     int
	Path   []int
	Detail string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s at AS %d path %v: %s", v.Kind, v.AS, v.Path, v.Detail)
}

// ValidationReport is the full control-plane export/topology audit.
type ValidationReport struct {
	StrictGR            bool
	ValleyFree          bool
	CommercialCycleFree bool
	ExportConsistent    bool
	Violations          []Violation
	// CommercialCycle is one directed customer->provider cycle when present.
	CommercialCycle []int
}

// IsClean reports whether the instance passes every structural check.
func (r ValidationReport) IsClean() bool {
	return r.ValleyFree && r.CommercialCycleFree && r.ExportConsistent && len(r.Violations) == 0
}

type valleyPhase int

const (
	phaseUp   valleyPhase = iota // following customer->provider edges
	phaseOver                    // traversing the single peer edge
	phaseDown                    // following provider->customer edges
)

// IsValleyFreePath checks the no-valley rule on a path oriented toward the
// destination: zero or more customer->provider ("up") edges, at most one peer
// ("over") edge, then zero or more provider->customer ("down") edges.
// Unrestricted (RelationUnknown) edges are exempt from valley checks.
func IsValleyFreePath(g *ASGraph, path []int) bool {
	if len(path) <= 1 {
		return true
	}
	phase := phaseUp
	for i := 0; i+1 < len(path); i++ {
		from, to := path[i], path[i+1]
		role, ok := TraversalRole(g, from, to)
		if !ok {
			return false
		}
		if role == RelationUnknown {
			continue // abstract gadget link: exempt
		}
		up, down, peer := edgeDirection(g, from, to)
		// Canonical valley-free shape: up* (peer?) down*. Once we leave the
		// up region via a peer or down edge, only down edges may follow.
		switch {
		case up:
			if phase != phaseUp {
				return false
			}
		case peer:
			if phase != phaseUp {
				return false
			}
			phase = phaseOver
		case down:
			phase = phaseDown
		}
	}
	return true
}

// edgeDirection classifies one propagation hop from -> to. Exactly one of
// up (customer->provider), down (provider->customer), peer is returned.
func edgeDirection(g *ASGraph, from, to int) (up, down, peer bool) {
	role, ok := TraversalRole(g, from, to)
	if !ok || role == RelationUnknown {
		return false, false, false
	}
	switch {
	case role == RelationCustomerToProvider:
		return true, false, false
	case isDownEdge(g, from, to):
		return false, true, false
	default:
		return false, false, true
	}
}

// isDownEdge reports whether propagation from -> to crosses a
// provider->customer edge (the reverse half of a customer-to-provider link).
func isDownEdge(g *ASGraph, from, to int) bool {
	l, ok := g.Link(from, to)
	if !ok || l.Relation != RelationCustomerToProvider {
		return false
	}
	return l.B == from && l.A == to
}

// tierOf computes the preference tier of a legal path at its choosing AS.
// The deciding edge is the first hop path[0] -> path[1], which records how
// the choosing AS learned the route.
func tierOf(g *ASGraph, path []int, override map[string]LocalPrefTier) LocalPrefTier {
	if override != nil {
		if t, ok := override[PathKey(path)]; ok {
			return t
		}
	}
	if len(path) < 2 {
		return PrefCustomer
	}
	switch {
	case isDownEdge(g, path[0], path[1]):
		return PrefCustomer
	case TraversalRoleMust(g, path[0], path[1]) == RelationCustomerToProvider:
		return PrefProvider
	default:
		return PrefPeer
	}
}

// TraversalRoleMust returns the directed relation or RelationUnknown.
func TraversalRoleMust(g *ASGraph, from, to int) Relation {
	r, _ := TraversalRole(g, from, to)
	return r
}

// ExpandPaths enumerates every simple, loop-free path from each AS to the
// origin that survives Gao-Rexford export filtering (or every simple path for
// unrestricted links). Paths are returned as strict total orders, best first.
func ExpandPaths(g *ASGraph, cfg PolicyConfig) (map[int][]PathRank, []Violation) {
	maxLen := cfg.MaxPathLength
	if maxLen <= 0 {
		maxLen = len(g.Nodes)
	}
	ranked := make(map[int][]PathRank, len(g.Nodes))
	var violations []Violation

	for _, node := range g.Nodes {
		if node == cfg.Origin {
			ranked[node] = []PathRank{{Path: []int{cfg.Origin}, Rank: 0, Tier: PrefCustomer, ValleyFree: true}}
			continue
		}
		seen := map[string]bool{}
		var kept []PathRank
		explicitRank := map[string]int{}
		for i, p := range cfg.ExtraPaths[node] {
			explicitRank[PathKey(p)] = i
		}
		admit := func(p []int, explicit bool) {
			key := PathKey(p)
			if seen[key] {
				return
			}
			if len(p) == 0 || p[0] != node || p[len(p)-1] != cfg.Origin || hasCycle(p) {
				violations = append(violations, Violation{
					Kind: "loop", AS: node, Path: CopyPath(p),
					Detail: "path is not a simple node->origin path (AS_PATH loop or bad endpoints)",
				})
				return
			}
			seen[key] = true
			vf := IsValleyFreePath(g, p)
			if cfg.StrictGR && !vf {
				violations = append(violations, Violation{
					Kind: "valley", AS: node, Path: CopyPath(p),
					Detail: "path goes up/over again after going down (Gao-Rexford valley)" +
						explicitLabel(explicit),
				})
				return
			}
			kept = append(kept, PathRank{
				Path:       CopyPath(p),
				Tier:       tierOf(g, p, overrideMap(cfg, node)),
				ValleyFree: vf,
				Explicit:   explicit,
			})
		}
		if !cfg.ExplicitOnly {
			for _, p := range enumerateSimplePaths(g, node, cfg.Origin, maxLen) {
				admit(p, false)
			}
		}
		for _, p := range cfg.ExtraPaths[node] {
			admit(p, true)
		}
		sort.SliceStable(kept, func(i, j int) bool {
			// Explicit gadget paths keep their caller-supplied order and
			// always rank ahead of automatically enumerated paths.
			if kept[i].Explicit || kept[j].Explicit {
				if kept[i].Explicit != kept[j].Explicit {
					return kept[i].Explicit
				}
				return explicitRank[PathKey(kept[i].Path)] < explicitRank[PathKey(kept[j].Path)]
			}
			return lessPath(kept[i], kept[j])
		})
		for i := range kept {
			kept[i].Rank = i
		}
		ranked[node] = kept
	}
	return ranked, violations
}

func explicitLabel(explicit bool) string {
	if explicit {
		return " [backup/local-pref override path]"
	}
	return ""
}

func overrideMap(cfg PolicyConfig, node int) map[string]LocalPrefTier {
	if cfg.TierFor == nil {
		return nil
	}
	return cfg.TierFor[node]
}

// lessPath encodes the strict policy order: local-pref tier first, then
// shortest AS path, then lexicographic tie breaking for determinism.
func lessPath(a, b PathRank) bool {
	if a.Tier != b.Tier {
		return a.Tier > b.Tier
	}
	if len(a.Path) != len(b.Path) {
		return len(a.Path) < len(b.Path)
	}
	for i := range a.Path {
		if a.Path[i] != b.Path[i] {
			return a.Path[i] < b.Path[i]
		}
	}
	return false
}

func hasCycle(p []int) bool {
	seen := map[int]bool{}
	for _, n := range p {
		if seen[n] {
			return true
		}
		seen[n] = true
	}
	return false
}

// enumerateSimplePaths returns all simple from->to paths in deterministic
// neighbor order, bounded by maxLen nodes.
func enumerateSimplePaths(g *ASGraph, from, to, maxLen int) [][]int {
	var out [][]int
	visited := map[int]bool{from: true}
	var dfs func(cur int, stack []int)
	dfs = func(cur int, stack []int) {
		if cur == to {
			out = append(out, append([]int(nil), stack...))
			return
		}
		if len(stack) >= maxLen {
			return
		}
		for _, e := range g.Neighbors(cur) {
			if visited[e.To] {
				continue
			}
			visited[e.To] = true
			dfs(e.To, append(stack, e.To))
			visited[e.To] = false
		}
	}
	dfs(from, []int{from})
	return out
}

// DetectCommercialCycle runs a directed DFS over customer->provider arcs and
// returns one directed cycle when the commercial hierarchy is cyclic.
func DetectCommercialCycle(g *ASGraph) []int {
	color := map[int]int{} // 0 white, 1 gray, 2 black
	var stack []int
	var found []int
	nodes := append([]int(nil), g.Nodes...)
	sort.Ints(nodes)
	var dfs func(n int) bool
	dfs = func(n int) bool {
		color[n] = 1
		stack = append(stack, n)
		for _, e := range g.Neighbors(n) {
			if e.Relation != RelationCustomerToProvider {
				continue
			}
			switch color[e.To] {
			case 0:
				if dfs(e.To) {
					return true
				}
			case 1:
				for i, x := range stack {
					if x == e.To {
						found = append(append([]int(nil), stack[i:]...), e.To)
						return true
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = 2
		return false
	}
	for _, n := range nodes {
		if color[n] == 0 && dfs(n) {
			return found
		}
	}
	return nil
}

// SimulateExportAudit replays Gao-Rexford export hop by hop along every
// candidate path. A route learned from a peer or provider may only be
// exported to a customer; customer-learned routes may be exported to anyone.
func SimulateExportAudit(g *ASGraph, ranked map[int][]PathRank) []Violation {
	var violations []Violation
	for _, node := range g.Nodes {
		for _, pr := range ranked[node] {
			p := pr.Path
			if len(p) < 2 {
				continue
			}
			// Route physically flows origin ... -> p[i+1] -> p[i] -> ... -> p[0].
			for i := len(p) - 2; i >= 1; i-- {
				exporter, receiver, upstream := p[i], p[i-1], p[i+1]
				learnRole, ok := TraversalRole(g, exporter, upstream)
				if !ok {
					continue
				}
				exportRole, ok2 := TraversalRole(g, exporter, receiver)
				if !ok2 || exportRole == RelationUnknown || learnRole == RelationUnknown {
					continue // abstract gadget links carry no GR obligations
				}
				learnedFromCustomer := isDownEdge(g, exporter, upstream)
				exportToCustomer := isDownEdge(g, exporter, receiver)
				if !learnedFromCustomer && !exportToCustomer {
					violations = append(violations, Violation{
						Kind: "illegal-export", AS: exporter, Path: CopyPath(p),
						Detail: fmt.Sprintf(
							"AS %d re-exports a %s-learned route to non-customer AS %d",
							exporter, learnedLabel(learnRole, g, exporter, upstream), receiver),
					})
				}
			}
		}
	}
	return violations
}

func learnedLabel(role Relation, g *ASGraph, from, to int) string {
	if isDownEdge(g, from, to) {
		return "customer"
	}
	if role == RelationPeer {
		return "peer"
	}
	return "provider"
}

// Validate runs the full structural audit for one configured instance.
func Validate(g *ASGraph, cfg PolicyConfig, ranked map[int][]PathRank, pathViolations []Violation) ValidationReport {
	rep := ValidationReport{
		StrictGR:            cfg.StrictGR,
		ValleyFree:          true,
		CommercialCycleFree: true,
		ExportConsistent:    true,
	}
	rep.Violations = append(rep.Violations, pathViolations...)
	for _, node := range g.Nodes {
		for _, pr := range ranked[node] {
			if !pr.ValleyFree {
				rep.ValleyFree = false
				rep.Violations = append(rep.Violations, Violation{
					Kind: "valley", AS: node, Path: CopyPath(pr.Path),
					Detail: "retained path violates valley-free routing",
				})
			}
		}
	}
	if cycle := DetectCommercialCycle(g); cycle != nil {
		rep.CommercialCycleFree = false
		rep.CommercialCycle = cycle
		rep.Violations = append(rep.Violations, Violation{
			Kind: "commercial-cycle", Path: cycle,
			Detail: "directed customer->provider hierarchy contains a cycle",
		})
	}
	if cfg.StrictGR {
		if ev := SimulateExportAudit(g, ranked); len(ev) > 0 {
			rep.ExportConsistent = false
			rep.Violations = append(rep.Violations, ev...)
		}
	}
	return rep
}
