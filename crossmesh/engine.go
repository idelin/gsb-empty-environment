package crossmesh

import (
	"errors"
	"fmt"
	"sort"
)

// MaxNodes is the supported graph size. All algorithms are polynomial in this
// bound; enumeration of root-to-leaf paths is never performed.
const MaxNodes = 2000

// MaxActions bounds the action universe to fit the compact action mask.
const MaxActions = 64

// chainMarker is the budget of the zero-length root marker installed on every
// trusted root. It represents the anchor itself and is never propagated across
// an edge, so non-root nodes cannot borrow root authority.
const chainMarker = -2

type outgoing struct {
	edgeID string
	target int
	cover  edgeCover
	limit  int
}

// Engine is a reusable, concurrency-unsafe evaluation host. After Build it
// answers authorization queries and accepts digest-frozen CAS revocations.
type Engine struct {
	nodeIndex map[string]int
	nodes     []string
	roots     map[int]struct{}

	edges      []DelegationEdge
	edgeByID   map[string]int
	outAdj     [][]outgoing
	edgeCover  []edgeCover
	scopeU     *scopeUniverse
	timeU      *timeUniverse
	actions    []string
	allActions actionMask

	auth   map[int]*vSet
	digest string
}

// New constructs and solves an engine over g.
func New(g Graph) (*Engine, error) {
	e := &Engine{
		nodeIndex: map[string]int{},
		roots:     map[int]struct{}{},
		edgeByID:  map[string]int{},
		auth:      map[int]*vSet{},
	}
	if err := e.validateAndIndex(g); err != nil {
		return nil, err
	}
	e.solve()
	return e, nil
}

// Digest returns the current signature-digest freeze of the complete edge set.
func (e *Engine) Digest() string { return e.digest }

func (e *Engine) validateAndIndex(g Graph) error {
	if len(g.Nodes) > MaxNodes {
		return fmt.Errorf("crossmesh: %d nodes exceed MaxNodes=%d", len(g.Nodes), MaxNodes)
	}
	if len(g.Nodes) == 0 {
		return errors.New("crossmesh: graph has no nodes")
	}
	seenNode := map[string]struct{}{}
	for i, n := range g.Nodes {
		if n == "" {
			return fmt.Errorf("crossmesh: node at index %d has empty id", i)
		}
		if _, dup := seenNode[n]; dup {
			return fmt.Errorf("crossmesh: duplicate node %q", n)
		}
		seenNode[n] = struct{}{}
		e.nodeIndex[n] = i
		e.nodes = append(e.nodes, n)
	}
	for _, r := range g.TrustedRoots {
		idx, ok := e.nodeIndex[r]
		if !ok {
			return fmt.Errorf("crossmesh: trusted root %q is not a node", r)
		}
		e.roots[idx] = struct{}{}
	}

	seenEdge := map[string]struct{}{}
	actionCount := map[string]struct{}{}
	for _, ed := range g.Edges {
		if ed.ID == "" {
			return errors.New("crossmesh: edge with empty id")
		}
		if _, dup := seenEdge[ed.ID]; dup {
			return fmt.Errorf("crossmesh: duplicate edge id %q", ed.ID)
		}
		seenEdge[ed.ID] = struct{}{}
		if _, ok := e.nodeIndex[ed.Issuer]; !ok {
			return fmt.Errorf("crossmesh: edge %q references unknown issuer %q", ed.ID, ed.Issuer)
		}
		if _, ok := e.nodeIndex[ed.Subject]; !ok {
			return fmt.Errorf("crossmesh: edge %q references unknown subject %q", ed.ID, ed.Subject)
		}
		if ed.Issuer == ed.Subject {
			// A self-issued edge can never lie on an acyclic trusted-root chain
			// and would only ever describe a rootless self-attestation.
			return fmt.Errorf("crossmesh: self-loop edge %q on node %q is not admissible", ed.ID, ed.Issuer)
		}
		if ed.MaxPathLen < 0 {
			return fmt.Errorf("crossmesh: edge %q has negative MaxPathLen", ed.ID)
		}
		if ed.NotBefore >= ed.NotAfter {
			return fmt.Errorf("crossmesh: edge %q has empty/negative validity interval", ed.ID)
		}
		for _, n := range ed.AllowedPrefixes {
			if n.Kind != KindDomain && n.Kind != KindURI {
				return fmt.Errorf("crossmesh: edge %q has scope of unknown kind", ed.ID)
			}
			if n.Value == "" {
				return fmt.Errorf("crossmesh: edge %q has empty scope value", ed.ID)
			}
		}
		for _, n := range ed.ExcludedSubtrees {
			if n.Kind != KindDomain && n.Kind != KindURI {
				return fmt.Errorf("crossmesh: edge %q has exclusion of unknown kind", ed.ID)
			}
		}
		for _, a := range ed.Actions {
			actionCount[a] = struct{}{}
		}
	}
	if len(actionCount) > MaxActions {
		return fmt.Errorf("crossmesh: %d distinct actions exceed MaxActions=%d",
			len(actionCount), MaxActions)
	}

	e.edges = append([]DelegationEdge(nil), g.Edges...)
	e.buildUniversesAndCovers()
	e.digest = freezeDigest(e.edges)
	return nil
}

func (e *Engine) buildUniversesAndCovers() {
	var prefixes []Name
	for _, ed := range e.edges {
		prefixes = append(prefixes, ed.AllowedPrefixes...)
		prefixes = append(prefixes, ed.ExcludedSubtrees...)
	}
	e.scopeU = buildScopeUniverse(prefixes)
	e.timeU = buildTimeUniverse(e.edges)
	var actionIdx map[string]uint8
	e.actions, actionIdx = buildActionUniverse(e.edges)
	for i := range e.actions {
		e.allActions |= 1 << uint(i)
	}

	n := len(e.nodes)
	e.outAdj = make([][]outgoing, n)
	e.edgeCover = make([]edgeCover, len(e.edges))
	for i, ed := range e.edges {
		cov := buildEdgeCover(ed, e.scopeU, e.timeU, actionIdx)
		e.edgeCover[i] = cov
		e.edgeByID[ed.ID] = i
		issuer := e.nodeIndex[ed.Issuer]
		subject := e.nodeIndex[ed.Subject]
		e.outAdj[issuer] = append(e.outAdj[issuer], outgoing{
			edgeID: ed.ID,
			target: subject,
			cover:  cov,
			limit:  ed.MaxPathLen,
		})
	}
}

// solve runs the polynomial per-column fixed point. Columns are solved
// independently and edge intersections happen inside one (cell, interval),
// which structurally prevents cross-chain dimension stitching. Rootless cycles
// never reach a trusted-root seed and therefore never gain any bits.
func (e *Engine) solve() {
	e.solveColumns()
}

// AuthorizedVoxels exposes the raw solved voxel set of a node for tooling and
// for the independent semantic acceptor.
func (e *Engine) AuthorizedVoxels(node string) (*vSet, error) {
	idx, ok := e.nodeIndex[node]
	if !ok {
		return nil, fmt.Errorf("crossmesh: unknown node %q", node)
	}
	return e.auth[idx].clone(), nil
}

// IsAuthorized reports whether a concrete (name, action, time) triple is
// supported by at least one acyclic trusted-root chain whose every hop admits
// the triple. Time comparisons use the half-open [NotBefore, NotAfter) rule.
func (e *Engine) IsAuthorized(node string, name Name, action string, at int64) (bool, error) {
	idx, ok := e.nodeIndex[node]
	if !ok {
		return false, fmt.Errorf("crossmesh: unknown node %q", node)
	}
	cell := e.scopeU.lookupCell(name)
	if cell == 0 {
		return false, nil
	}
	bounds := e.timeU.bounds
	if len(bounds) < 2 || at < bounds[0] || at >= bounds[len(bounds)-1] {
		return false, nil
	}
	ti := sort.Search(len(bounds)-1, func(i int) bool { return at < bounds[i+1] })
	actionBit, ok := indexOf(e.actions, action)
	if !ok {
		return false, nil
	}
	mask, ok := e.auth[idx].m[vKey{cell: cell, time: ti}]
	return ok && mask&(1<<uint(actionBit)) != 0, nil
}

func indexOf(ss []string, target string) (int, bool) {
	i := sort.SearchStrings(ss, target)
	if i < len(ss) && ss[i] == target {
		return i, true
	}
	return 0, false
}

// Slices renders the solved voxel set of a node as a list of disjoint
// three-dimensional slices. Each slice binds one scope cell, one action and
// one maximal time window. Because columns are solved independently and each
// action bit is emitted separately, a combination no single chain granted can
// never appear, and the returned slices are pairwise disjoint.
func (e *Engine) Slices(node string) ([]Slice, error) {
	idx, ok := e.nodeIndex[node]
	if !ok {
		return nil, fmt.Errorf("crossmesh: unknown node %q", node)
	}
	return e.renderSlices(e.auth[idx]), nil
}

func (e *Engine) renderSlices(vs *vSet) []Slice {
	if vs == nil || vs.isEmpty() {
		return nil
	}
	type groupKey struct {
		cell int
		mask actionMask
	}
	groups := map[groupKey][]int{}
	for k, mask := range vs.m {
		// One key per exact (cell, action-mask) column: action bits from
		// different chains are never ORed into a wider slice.
		gk := groupKey{cell: k.cell, mask: mask}
		groups[gk] = append(groups[gk], k.time)
	}

	keys := make([]groupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].cell != keys[j].cell {
			return keys[i].cell < keys[j].cell
		}
		return keys[i].mask < keys[j].mask
	})

	var out []Slice
	for _, gk := range keys {
		times := groups[gk]
		sort.Ints(times)
		cell := e.scopeU.cells[gk.cell]
		var acts [][]string
		for i, a := range e.actions {
			if gk.mask&(1<<uint(i)) != 0 {
				// One action per emitted slice even when a column carries
				// several bits: multiple bits mean independently supported
				// chains, never one chain that grants the combined set.
				acts = append(acts, []string{a})
			}
		}
		// Merge adjacent atomic intervals into maximal half-open windows.
		start := times[0]
		prev := times[0]
		flush := func(lo, hi int) {
			for _, single := range acts {
				out = append(out, Slice{
					Cells:   []ScopeCell{cell},
					Actions: append([]string(nil), single...),
					Window: TimeWindow{
						From: e.timeU.bounds[lo],
						To:   e.timeU.bounds[hi+1],
					},
				})
			}
		}
		for _, t := range times[1:] {
			if t == prev+1 {
				prev = t
				continue
			}
			flush(start, prev)
			start, prev = t, t
		}
		flush(start, prev)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Window.From != out[j].Window.From {
			return out[i].Window.From < out[j].Window.From
		}
		if out[i].Cells[0].Prefix.Value != out[j].Cells[0].Prefix.Value {
			return out[i].Cells[0].Prefix.Value < out[j].Cells[0].Prefix.Value
		}
		return out[i].Actions[0] < out[j].Actions[0]
	})
	return out
}
