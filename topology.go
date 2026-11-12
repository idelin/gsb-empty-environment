package taskpkg

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrNonManifold is returned when the input patch graph is not a
	// manifold (an edge shared by more than two patches, or a vertex
	// whose link is not a single path or cycle).
	ErrNonManifold = errors.New("taskpkg: non-manifold mesh")
	// ErrNonOrientable is returned when two patches traverse their
	// shared edge in the same direction.
	ErrNonOrientable = errors.New("taskpkg: non-orientable mesh")
	// ErrInvalidLevel is returned for negative levels or segment counts
	// that are not a positive power of two.
	ErrInvalidLevel = errors.New("taskpkg: invalid tessellation level")
	// ErrInvalidPatch is returned for malformed patch records.
	ErrInvalidPatch = errors.New("taskpkg: invalid patch")
)

// Vec3 is a 3D position or direction.
type Vec3 [3]float64

func (a Vec3) Add(b Vec3) Vec3 { return Vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a Vec3) Sub(b Vec3) Vec3 { return Vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a Vec3) Scale(s float64) Vec3 {
	return Vec3{a[0] * s, a[1] * s, a[2] * s}
}
func (a Vec3) Dot(b Vec3) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{
		a[1]*b[2] - a[2]*b[1],
		a[2]*b[0] - a[0]*b[2],
		a[0]*b[1] - a[1]*b[0],
	}
}

// EdgeID canonically identifies an undirected mesh edge by its endpoint
// vertex IDs, lower ID first.
type EdgeID struct {
	Lo, Hi int
}

// NewEdgeID builds the canonical ID of the edge between vertices a and b.
func NewEdgeID(a, b int) EdgeID {
	if a > b {
		a, b = b, a
	}
	return EdgeID{Lo: a, Hi: b}
}

// Patch is a single quadrilateral patch. Corners are vertex IDs in
// counter-clockwise order as seen from the patch's front (normal) side.
// Level is the base-2 logarithm of the tessellation segments per edge, so
// the patch edge is split into 1<<Level segments; Level must be >= 0.
type Patch struct {
	ID      int
	Corners [4]int
	Level   int
}

// LevelFromSegments converts a segment count into a log2 level, rejecting
// values that are not a positive power of two.
func LevelFromSegments(segments int) (int, error) {
	if segments < 1 || segments&(segments-1) != 0 {
		return 0, fmt.Errorf("%w: %d segments is not a positive power of two", ErrInvalidLevel, segments)
	}
	level := 0
	for segments > 1 {
		segments >>= 1
		level++
	}
	return level, nil
}

// edgeUse records one directed use of an undirected edge by a patch.
type edgeUse struct {
	patch    int // index into Topology.patches
	edge     int // patch-local edge index 0..3
	from, to int // directed endpoint vertex IDs
}

// Topology is a validated, orientable manifold quad patch graph.
type Topology struct {
	patches   []Patch
	patchIdx  map[int]int
	edges     map[EdgeID][]edgeUse
	edgeOrder []EdgeID
}

// NewTopology validates the patch set and builds the shared-edge topology.
// It rejects non-manifold and non-orientable inputs.
func NewTopology(patches []Patch) (*Topology, error) {
	if len(patches) == 0 {
		return nil, fmt.Errorf("%w: no patches", ErrInvalidPatch)
	}
	t := &Topology{
		patches:  make([]Patch, len(patches)),
		patchIdx: make(map[int]int, len(patches)),
		edges:    make(map[EdgeID][]edgeUse),
	}
	copy(t.patches, patches)
	for i, p := range t.patches {
		if p.Level < 0 {
			return nil, fmt.Errorf("%w: patch %d has negative level %d", ErrInvalidLevel, p.ID, p.Level)
		}
		if _, dup := t.patchIdx[p.ID]; dup {
			return nil, fmt.Errorf("%w: duplicate patch ID %d", ErrInvalidPatch, p.ID)
		}
		t.patchIdx[p.ID] = i
		for k := 0; k < 4; k++ {
			for l := k + 1; l < 4; l++ {
				if p.Corners[k] == p.Corners[l] {
					return nil, fmt.Errorf("%w: patch %d repeats corner %d", ErrInvalidPatch, p.ID, p.Corners[k])
				}
			}
		}
		for e := 0; e < 4; e++ {
			a, b := p.Corners[e], p.Corners[(e+1)%4]
			id := NewEdgeID(a, b)
			t.edges[id] = append(t.edges[id], edgeUse{patch: i, edge: e, from: a, to: b})
		}
	}
	for id, uses := range t.edges {
		if len(uses) > 2 {
			return nil, fmt.Errorf("%w: edge %v shared by %d patches", ErrNonManifold, id, len(uses))
		}
		if len(uses) == 2 {
			u0, u1 := uses[0], uses[1]
			if u0.from != u1.to || u0.to != u1.from {
				return nil, fmt.Errorf("%w: edge %v traversed in the same direction by adjacent patches", ErrNonOrientable, id)
			}
		}
		t.edgeOrder = append(t.edgeOrder, id)
	}
	sort.Slice(t.edgeOrder, func(a, b int) bool {
		if t.edgeOrder[a].Lo != t.edgeOrder[b].Lo {
			return t.edgeOrder[a].Lo < t.edgeOrder[b].Lo
		}
		return t.edgeOrder[a].Hi < t.edgeOrder[b].Hi
	})
	if err := t.checkVertexLinks(); err != nil {
		return nil, err
	}
	return t, nil
}

// checkVertexLinks verifies that the link of every vertex is a single path
// (boundary) or a single cycle (interior), rejecting non-manifold vertices
// such as two fans touching at one point.
func (t *Topology) checkVertexLinks() error {
	type linkEdge struct{ a, b int }
	link := map[int][]linkEdge{}
	for _, p := range t.patches {
		for k := 0; k < 4; k++ {
			v := p.Corners[k]
			prev := p.Corners[(k+3)%4]
			next := p.Corners[(k+1)%4]
			link[v] = append(link[v], linkEdge{prev, next})
		}
	}
	for v, edges := range link {
		adj := map[int][]int{}
		for _, le := range edges {
			adj[le.a] = append(adj[le.a], le.b)
			adj[le.b] = append(adj[le.b], le.a)
		}
		deg1 := 0
		for x, ns := range adj {
			if len(ns) > 2 {
				return fmt.Errorf("%w: link of vertex %d has degree %d at vertex %d", ErrNonManifold, v, len(ns), x)
			}
			if len(ns) == 1 {
				deg1++
			}
		}
		if deg1 != 0 && deg1 != 2 {
			return fmt.Errorf("%w: link of vertex %d is neither a path nor a cycle", ErrNonManifold, v)
		}
		start := edges[0].a
		seen := map[int]bool{start: true}
		queue := []int{start}
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			for _, y := range adj[x] {
				if !seen[y] {
					seen[y] = true
					queue = append(queue, y)
				}
			}
		}
		if len(seen) != len(adj) {
			return fmt.Errorf("%w: link of vertex %d is disconnected", ErrNonManifold, v)
		}
	}
	return nil
}

// PropagateLevels raises patch levels until every pair of patches sharing
// an edge differs by at most one level. Levels only ever increase, so the
// result is the unique least fixpoint; it is idempotent and deterministic.
func (t *Topology) PropagateLevels() {
	for {
		changed := false
		for _, id := range t.edgeOrder {
			uses := t.edges[id]
			if len(uses) != 2 {
				continue
			}
			a := &t.patches[uses[0].patch]
			b := &t.patches[uses[1].patch]
			switch {
			case a.Level > b.Level+1:
				b.Level = a.Level - 1
				changed = true
			case b.Level > a.Level+1:
				a.Level = b.Level - 1
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

// Patches returns a copy of the current patches (with propagated levels).
func (t *Topology) Patches() []Patch {
	out := make([]Patch, len(t.patches))
	copy(out, t.patches)
	return out
}

// LevelOf returns the current level of a patch.
func (t *Topology) LevelOf(patchID int) (int, bool) {
	i, ok := t.patchIdx[patchID]
	if !ok {
		return 0, false
	}
	return t.patches[i].Level, true
}

// MaxAdjacentLevelDiff returns the maximum level difference across any
// shared edge; after PropagateLevels it is at most 1.
func (t *Topology) MaxAdjacentLevelDiff() int {
	maxDiff := 0
	for _, id := range t.edgeOrder {
		uses := t.edges[id]
		if len(uses) != 2 {
			continue
		}
		d := t.patches[uses[0].patch].Level - t.patches[uses[1].patch].Level
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	return maxDiff
}

// edgeLevel is the tessellation level of an edge itself: the maximum level
// of the patches using it, so both sides agree on the edge segmentation.
func (t *Topology) edgeLevel(id EdgeID) int {
	level := 0
	for _, u := range t.edges[id] {
		if lv := t.patches[u.patch].Level; lv > level {
			level = lv
		}
	}
	return level
}
