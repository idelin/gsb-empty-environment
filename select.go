package netsec

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Reason is a stable machine-readable rejection code.
type Reason = string

const (
	ReasonTooManyNodes               Reason = "too_many_nodes"
	ReasonEmptyVersion               Reason = "empty_version_id"
	ReasonDuplicateVersion           Reason = "duplicate_version"
	ReasonUnknownCurrentVersion      Reason = "unknown_current_version"
	ReasonUnknownEdgeEndpoint        Reason = "unknown_edge_endpoint"
	ReasonRevokedVersion             Reason = "revoked_version"
	ReasonInvalidLevelSignature      Reason = "invalid_level_signature"
	ReasonIncompatibleVersion        Reason = "incompatible_version"
	ReasonSecurityLevelDecrease      Reason = "security_level_decrease"
	ReasonUnreachableVersion         Reason = "unreachable_version"
	ReasonPathBlockedByRevoked       Reason = "path_blocked_by_revoked_version"
	ReasonPathBlockedBySignature     Reason = "path_blocked_by_invalid_signature"
	ReasonPathBlockedByCompatibility Reason = "path_blocked_by_incompatible_version"
	ReasonPathBlockedByLevelDecrease Reason = "path_blocked_by_security_level_decrease"
	ReasonNotMinimumCost             Reason = "not_minimum_migration_cost"
	ReasonNoAlternativeVersion       Reason = "no_alternative_version"
)

var (
	// ErrInvalidManifest marks a structurally invalid or oversized manifest.
	ErrInvalidManifest = errors.New("netsec: invalid version manifest")
	// ErrNoSafeVersion means no candidate satisfies the migration policy.
	ErrNoSafeVersion = errors.New("netsec: no safe reachable version")
)

// Rejection explains why one version was not selected.
type Rejection struct {
	Version VersionID `json:"version"`
	Reason  Reason    `json:"reason"`
	Detail  string    `json:"detail,omitempty"`
}

// Selection is the result of a migration search. Path includes the current
// version and every intermediate version through to Target.
type Selection struct {
	Current    VersionID     `json:"current"`
	Path       []VersionID   `json:"path,omitempty"`
	Target     VersionID     `json:"target,omitempty"`
	Level      SecurityLevel `json:"level"`
	Cost       uint64        `json:"cost"`
	Rejections []Rejection   `json:"rejections,omitempty"`
	Err        error         `json:"-"`
}

// Result and Path are convenience aliases for the selection result.
type (
	Result = Selection
	Path   = []VersionID
)

// Selected reports whether a target was found.
func (s Selection) Selected() bool {
	return s.Err == nil && s.Target != ""
}

// SelectionError carries the rejection list together with the category of
// failure, so callers can use errors.Is while still rendering reasons.
type SelectionError struct {
	Cause      error
	Rejections []Rejection
}

func (e *SelectionError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if len(e.Rejections) == 0 {
		return e.Cause.Error()
	}
	rejection := e.Rejections[0]
	return fmt.Sprintf("%v: version %q rejected (%s: %s)", e.Cause, rejection.Version, rejection.Reason, rejection.Detail)
}

// Unwrap enables errors.Is(err, ErrNoSafeVersion) and
// errors.Is(err, ErrInvalidManifest).
func (e *SelectionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Select chooses the lowest-cost reachable non-revoked version whose security
// level never decreases along the migration path. The current version itself
// is a candidate at zero cost when it is still safe.
//
// The optional environment supplies the values used by compatibility rules.
// Omitted or nil means an empty environment.
func Select(m Manifest, current VersionID, environments ...Environment) (Selection, error) {
	return selectVersion(m, current, mergeEnvironments(environments), true)
}

// SelectVersion is an alias for Select.
func SelectVersion(m Manifest, current VersionID, environments ...Environment) (Selection, error) {
	return Select(m, current, environments...)
}

// SelectUpgrade is like Select but excludes the current version as a target.
// It is the usual choice for an update host that must leave a revoked or
// otherwise unacceptable current version.
func SelectUpgrade(m Manifest, current VersionID, environments ...Environment) (Selection, error) {
	return selectVersion(m, current, mergeEnvironments(environments), false)
}

// Select chooses a target using method-style access.
func (m Manifest) Select(current VersionID, environments ...Environment) (Selection, error) {
	return Select(m, current, environments...)
}

// SelectVersion is an alias for Select.
func (m Manifest) SelectVersion(current VersionID, environments ...Environment) (Selection, error) {
	return Select(m, current, environments...)
}

// SelectUpgrade excludes the current version as a target.
func (m Manifest) SelectUpgrade(current VersionID, environments ...Environment) (Selection, error) {
	return SelectUpgrade(m, current, environments...)
}

func mergeEnvironments(environments []Environment) Environment {
	switch len(environments) {
	case 0:
		return nil
	case 1:
		return environments[0]
	}
	merged := make(Environment)
	for _, environment := range environments {
		for key, value := range environment {
			merged[key] = value
		}
	}
	return merged
}

func selectVersion(m Manifest, current VersionID, env Environment, allowCurrent bool) (Selection, error) {
	selection := Selection{Current: current, Path: []VersionID{}}

	graph, rejections, err := newGraph(m, current, env)
	if err != nil {
		selection.Rejections = rejections
		selection.Err = err
		return selection, err
	}

	if graph.nodes[current].signatureErr != nil {
		rejection := Rejection{
			Version: current,
			Reason:  ReasonInvalidLevelSignature,
			Detail:  "current version security level is not authentic: " + graph.nodes[current].signatureErr.Error(),
		}
		selection.Rejections = []Rejection{rejection}
		err := &SelectionError{Cause: ErrNoSafeVersion, Rejections: selection.Rejections}
		selection.Err = err
		return selection, err
	}

	choice := graph.search(allowCurrent)
	if choice == nil {
		rejections := graph.rejections(nil, allowCurrent)
		if len(rejections) == 0 {
			rejections = []Rejection{{
				Version: current,
				Reason:  ReasonNoAlternativeVersion,
				Detail:  "no version other than the current version can be selected",
			}}
		}
		err := &SelectionError{Cause: ErrNoSafeVersion, Rejections: rejections}
		selection.Rejections = rejections
		selection.Err = err
		return selection, err
	}

	selection.Path = append([]VersionID(nil), choice.path...)
	selection.Target = choice.id
	selection.Level = graph.nodes[choice.id].node.Level.Level
	selection.Cost = choice.cost
	selection.Rejections = graph.rejections(choice, allowCurrent)
	return selection, nil
}

type graphNode struct {
	node          Node
	signatureErr  error
	compatible    bool
	compatibility []string
}

type versionGraph struct {
	current VersionID
	nodes   map[VersionID]*graphNode
	order   []VersionID
	adj     map[VersionID][]Edge

	paths            map[VersionID]pathState
	pathsReady       bool
	reachAll         map[VersionID]bool
	reachNoRevoked   map[VersionID]bool
	reachSigned      map[VersionID]bool
	reachCompatible  map[VersionID]bool
	reachNonDecrease map[VersionID]bool
	diagnosticsReady bool
}

func newGraph(m Manifest, current VersionID, env Environment) (*versionGraph, []Rejection, error) {
	if len(m.Nodes) == 0 {
		rejection := Rejection{Reason: ReasonUnreachableVersion, Detail: "manifest contains no versions"}
		return nil, []Rejection{rejection}, fmt.Errorf("%w: %s", ErrInvalidManifest, rejection.Detail)
	}
	if len(m.Nodes) > MaxNodes {
		rejection := Rejection{
			Reason: ReasonTooManyNodes,
			Detail: fmt.Sprintf("manifest has %d versions, maximum is %d", len(m.Nodes), MaxNodes),
		}
		return nil, []Rejection{rejection}, fmt.Errorf("%w: %s", ErrInvalidManifest, rejection.Detail)
	}

	graph := &versionGraph{
		current: current,
		nodes:   make(map[VersionID]*graphNode, len(m.Nodes)),
		order:   make([]VersionID, 0, len(m.Nodes)),
		adj:     make(map[VersionID][]Edge, len(m.Nodes)),
	}

	for _, node := range m.Nodes {
		if node.Version == "" {
			rejection := Rejection{Reason: ReasonEmptyVersion, Detail: "manifest contains an empty version ID"}
			return nil, []Rejection{rejection}, fmt.Errorf("%w: %s", ErrInvalidManifest, rejection.Detail)
		}
		if _, exists := graph.nodes[node.Version]; exists {
			rejection := Rejection{
				Version: node.Version,
				Reason:  ReasonDuplicateVersion,
				Detail:  "version appears more than once in the manifest",
			}
			return nil, []Rejection{rejection}, fmt.Errorf("%w: %s", ErrInvalidManifest, rejection.Detail)
		}
		graph.nodes[node.Version] = &graphNode{node: node}
		graph.order = append(graph.order, node.Version)
	}

	if _, exists := graph.nodes[current]; !exists {
		rejection := Rejection{
			Version: current,
			Reason:  ReasonUnknownCurrentVersion,
			Detail:  "current version is not present in the manifest",
		}
		return nil, []Rejection{rejection}, fmt.Errorf("%w: %s", ErrInvalidManifest, rejection.Detail)
	}

	for _, edge := range m.Edges {
		if _, exists := graph.nodes[edge.From]; !exists {
			rejection := Rejection{
				Version: edge.From,
				Reason:  ReasonUnknownEdgeEndpoint,
				Detail:  fmt.Sprintf("edge source %q is not in the manifest", edge.From),
			}
			return nil, []Rejection{rejection}, fmt.Errorf("%w: %s", ErrInvalidManifest, rejection.Detail)
		}
		if _, exists := graph.nodes[edge.To]; !exists {
			rejection := Rejection{
				Version: edge.To,
				Reason:  ReasonUnknownEdgeEndpoint,
				Detail:  fmt.Sprintf("edge destination %q is not in the manifest", edge.To),
			}
			return nil, []Rejection{rejection}, fmt.Errorf("%w: %s", ErrInvalidManifest, rejection.Detail)
		}
		graph.adj[edge.From] = append(graph.adj[edge.From], edge)
	}

	for _, version := range graph.order {
		node := graph.nodes[version]
		if m.VerifyLevel != nil {
			node.signatureErr = m.VerifyLevel(node.node.Version, node.node.Level)
		}
		node.compatible, node.compatibility = node.node.Compatibility.Match(env)
	}

	for version := range graph.adj {
		edges := graph.adj[version]
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].To != edges[j].To {
				return edges[i].To < edges[j].To
			}
			return edges[i].Cost < edges[j].Cost
		})
		graph.adj[version] = edges
	}
	sort.Strings(graph.order)

	return graph, nil, nil
}

type pathState struct {
	id   VersionID
	cost uint64
	path []VersionID
}

func (s pathState) hops() int {
	return len(s.path) - 1
}

func (s pathState) betterThan(other pathState) bool {
	if s.cost != other.cost {
		return s.cost < other.cost
	}
	if s.hops() != other.hops() {
		return s.hops() < other.hops()
	}
	return lessPath(s.path, other.path)
}

type stateHeap []pathState

func (h stateHeap) Len() int { return len(h) }

func (h stateHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.hops() != b.hops() {
		return a.hops() < b.hops()
	}
	if lessPath(a.path, b.path) {
		return true
	}
	if lessPath(b.path, a.path) {
		return false
	}
	return a.id < b.id
}

func (h stateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *stateHeap) Push(value any) {
	*h = append(*h, value.(pathState))
}

func (h *stateHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

func (g *versionGraph) search(allowCurrent bool) *pathState {
	best := g.shortestPaths()

	var selected *pathState
	for version, state := range best {
		node := g.nodes[version]
		if version == g.current {
			if !allowCurrent || node.node.Revoked || node.signatureErr != nil || !node.compatible {
				continue
			}
		} else if node.node.Revoked || node.signatureErr != nil || !node.compatible {
			continue
		}

		candidate := state
		if selected == nil || betterTarget(candidate, *selected, g) {
			selected = &candidate
		}
	}
	return selected
}

func sameState(a, b pathState) bool {
	if a.id != b.id || a.cost != b.cost || len(a.path) != len(b.path) {
		return false
	}
	for i := range a.path {
		if a.path[i] != b.path[i] {
			return false
		}
	}
	return true
}

func betterTarget(candidate, selected pathState, graph *versionGraph) bool {
	if candidate.cost != selected.cost {
		return candidate.cost < selected.cost
	}
	candidateLevel := graph.nodes[candidate.id].node.Level.Level
	selectedLevel := graph.nodes[selected.id].node.Level.Level
	if candidateLevel != selectedLevel {
		return candidateLevel > selectedLevel
	}
	if candidate.hops() != selected.hops() {
		return candidate.hops() < selected.hops()
	}
	if candidate.id != selected.id {
		return candidate.id < selected.id
	}
	return lessPath(candidate.path, selected.path)
}

func appendPath(path []VersionID, version VersionID) []VersionID {
	next := make([]VersionID, len(path)+1)
	copy(next, path)
	next[len(path)] = version
	return next
}

func lessPath(a, b []VersionID) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func (g *versionGraph) rejections(selected *pathState, allowCurrent bool) []Rejection {
	var reasons []Rejection
	for _, version := range g.order {
		if selected != nil && version == selected.id {
			continue
		}
		if version == g.current && !allowCurrent {
			continue
		}

		node := g.nodes[version]
		local := g.localRejections(node)
		if len(local) > 0 {
			reasons = append(reasons, local...)
			continue
		}
		if version == g.current {
			if selected != nil && selected.id != g.current {
				reasons = append(reasons, Rejection{
					Version: version,
					Reason:  ReasonNotMinimumCost,
					Detail:  fmt.Sprintf("migration cost 0 is not lower than selected target %q", selected.id),
				})
			}
			continue
		}

		reasons = append(reasons, g.pathRejections(node, selected)...)
	}
	return reasons
}

func (g *versionGraph) localRejections(node *graphNode) []Rejection {
	var reasons []Rejection
	if node.node.Revoked {
		reasons = append(reasons, Rejection{
			Version: node.node.Version,
			Reason:  ReasonRevokedVersion,
			Detail:  "version has been revoked",
		})
	}
	if node.signatureErr != nil {
		reasons = append(reasons, Rejection{
			Version: node.node.Version,
			Reason:  ReasonInvalidLevelSignature,
			Detail:  "security level signature was rejected: " + node.signatureErr.Error(),
		})
	}
	if !node.compatible {
		reasons = append(reasons, Rejection{
			Version: node.node.Version,
			Reason:  ReasonIncompatibleVersion,
			Detail:  "compatibility conditions not met: " + strings.Join(node.compatibility, "; "),
		})
	}
	if node.node.Version != g.current && node.node.Level.Level < g.nodes[g.current].node.Level.Level {
		reasons = append(reasons, Rejection{
			Version: node.node.Version,
			Reason:  ReasonSecurityLevelDecrease,
			Detail: fmt.Sprintf(
				"security level %d is lower than current level %d",
				node.node.Level.Level,
				g.nodes[g.current].node.Level.Level,
			),
		})
	}
	return reasons
}

func (g *versionGraph) pathRejections(node *graphNode, selected *pathState) []Rejection {
	reachAll, reachNoRevoked, reachSigned, reachCompatible, reachNonDecrease := g.reachability()

	if !reachAll[node.node.Version] {
		return []Rejection{{
			Version: node.node.Version,
			Reason:  ReasonUnreachableVersion,
			Detail:  fmt.Sprintf("no legal manifest path from version %q", g.current),
		}}
	}
	if !reachNoRevoked[node.node.Version] {
		return []Rejection{{
			Version: node.node.Version,
			Reason:  ReasonPathBlockedByRevoked,
			Detail:  "every legal path passes through a revoked version",
		}}
	}
	if !reachSigned[node.node.Version] {
		return []Rejection{{
			Version: node.node.Version,
			Reason:  ReasonPathBlockedBySignature,
			Detail:  "every legal path passes through a version with an invalid security signature",
		}}
	}
	if !reachCompatible[node.node.Version] {
		return []Rejection{{
			Version: node.node.Version,
			Reason:  ReasonPathBlockedByCompatibility,
			Detail:  "every legal path passes through an incompatible version",
		}}
	}
	if !reachNonDecrease[node.node.Version] {
		return []Rejection{{
			Version: node.node.Version,
			Reason:  ReasonPathBlockedByLevelDecrease,
			Detail:  "every legal path contains a security level decrease",
		}}
	}

	if selected == nil {
		return []Rejection{{
			Version: node.node.Version,
			Reason:  ReasonUnreachableVersion,
			Detail:  "no candidate satisfies the migration policy",
		}}
	}
	g.ensurePaths()
	if state, ok := g.paths[node.node.Version]; ok {
		return []Rejection{{
			Version: node.node.Version,
			Reason:  ReasonNotMinimumCost,
			Detail: fmt.Sprintf(
				"migration cost %d is not lower than selected target %q at cost %d",
				state.cost,
				selected.id,
				selected.cost,
			),
		}}
	}
	return []Rejection{{
		Version: node.node.Version,
		Reason:  ReasonUnreachableVersion,
		Detail:  "no path satisfies the migration policy",
	}}
}

func (g *versionGraph) ensurePaths() {
	if !g.pathsReady {
		g.paths = g.computeShortestPaths()
		g.pathsReady = true
	}
}

func (g *versionGraph) shortestPaths() map[VersionID]pathState {
	g.ensurePaths()
	return g.paths
}

func (g *versionGraph) computeShortestPaths() map[VersionID]pathState {
	best := map[VersionID]pathState{
		g.current: {id: g.current, cost: 0, path: []VersionID{g.current}},
	}
	queue := &stateHeap{best[g.current]}
	heap.Init(queue)

	for queue.Len() > 0 {
		current := heap.Pop(queue).(pathState)
		if known, ok := best[current.id]; !ok || !sameState(known, current) {
			continue
		}
		from := g.nodes[current.id]
		for _, edge := range g.adj[current.id] {
			if edge.To == g.current {
				continue
			}
			to := g.nodes[edge.To]
			if to.node.Revoked || to.signatureErr != nil || !to.compatible ||
				to.node.Level.Level < from.node.Level.Level {
				continue
			}
			if edge.Cost > math.MaxUint64-current.cost {
				continue
			}
			next := pathState{
				id:   edge.To,
				cost: current.cost + edge.Cost,
				path: appendPath(current.path, edge.To),
			}
			known, exists := best[edge.To]
			if !exists || next.betterThan(known) {
				best[edge.To] = next
				heap.Push(queue, next)
			}
		}
	}
	return best
}

func (g *versionGraph) reachability() (
	reachAll map[VersionID]bool,
	reachNoRevoked map[VersionID]bool,
	reachSigned map[VersionID]bool,
	reachCompatible map[VersionID]bool,
	reachNonDecrease map[VersionID]bool,
) {
	if g.diagnosticsReady {
		return g.reachAll, g.reachNoRevoked, g.reachSigned, g.reachCompatible, g.reachNonDecrease
	}

	g.reachAll = g.reach(func(_ *graphNode, _ *graphNode) bool { return true })
	g.reachNoRevoked = g.reach(func(_ *graphNode, to *graphNode) bool {
		return !to.node.Revoked
	})
	g.reachSigned = g.reach(func(_ *graphNode, to *graphNode) bool {
		return !to.node.Revoked && to.signatureErr == nil
	})
	g.reachCompatible = g.reach(func(_ *graphNode, to *graphNode) bool {
		return !to.node.Revoked && to.signatureErr == nil && to.compatible
	})
	g.reachNonDecrease = g.reach(func(from, to *graphNode) bool {
		return !to.node.Revoked &&
			to.signatureErr == nil &&
			to.compatible &&
			to.node.Level.Level >= from.node.Level.Level
	})
	g.diagnosticsReady = true
	return g.reachAll, g.reachNoRevoked, g.reachSigned, g.reachCompatible, g.reachNonDecrease
}

func (g *versionGraph) reach(allowed func(from, to *graphNode) bool) map[VersionID]bool {
	reached := map[VersionID]bool{g.current: true}
	queue := []VersionID{g.current}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		for _, edge := range g.adj[from] {
			if edge.To == g.current || reached[edge.To] {
				continue
			}
			if allowed(g.nodes[from], g.nodes[edge.To]) {
				reached[edge.To] = true
				queue = append(queue, edge.To)
			}
		}
	}
	return reached
}
