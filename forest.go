package netsec

import (
	"errors"
	"slices"
	"strings"
)

// NodeID identifies a resource node. The zero value is never a valid node.
type NodeID string

var (
	// ErrNodeNotFound is returned when a node id does not exist in the graph.
	ErrNodeNotFound = errors.New("netsec: node not found")
	// ErrNodeExists is returned when creating a node that already exists.
	ErrNodeExists = errors.New("netsec: node already exists")
	// ErrCycle is returned when a move would place a node inside its own subtree.
	ErrCycle = errors.New("netsec: move would create a cycle")
	// ErrTxnClosed is returned when a move transaction is committed or rolled
	// back more than once.
	ErrTxnClosed = errors.New("netsec: move transaction already finished")
)

// Tag is a single security label key/value pair.
type Tag struct {
	Key   string
	Value string
}

// Provenance records how an inherited tag reached a node.
type Provenance struct {
	// Key is the tag key carried by the inherited value.
	Key string
	// Source is the ancestor that provided the explicit value.
	Source NodeID
	// Value is the explicit value declared at Source.
	Value string
	// Path is the chain of node ids from the tagged node up to Source,
	// including both endpoints.
	Path []NodeID
	// Blocked is true when the chain contains a cut-off marker. In that case
	// Source identifies the node that set the nearest marker, Value is empty,
	// and the tag must not be applied to the node or its descendants until an
	// explicit tag or an unblock reopens the key.
	Blocked bool
}

// EffectiveTag is a resolved label together with its origin.
type EffectiveTag struct {
	Key    string
	Value  string
	Source NodeID
	// Explicit is true when the value comes from a tag set on the node itself.
	Explicit bool
}

type node struct {
	id       NodeID
	parent   NodeID
	children []NodeID
	// explicit maps tag key to value; only directly assigned tags live here.
	explicit map[string]string
	// inherited caches inherited values keyed by tag key. An entry whose
	// provenance is marked blocked records a cut-off marker on the chain.
	inherited map[string]Provenance
	// blocked maps tag key to the cut-off marker state for this node.
	blocked map[string]bool
}

func newNode(id NodeID) *node {
	return &node{
		id:        id,
		explicit:  make(map[string]string),
		inherited: make(map[string]Provenance),
		blocked:   make(map[string]bool),
	}
}

type graph struct {
	nodes map[NodeID]*node
	// roots keeps forest roots in insertion order.
	roots []NodeID
}

func newGraph() *graph {
	return &graph{nodes: make(map[NodeID]*node)}
}

func clonePath(path []NodeID) []NodeID {
	if len(path) == 0 {
		return nil
	}
	return append([]NodeID(nil), path...)
}

// ancestorPath returns the path from node id up to the forest root, both
// inclusive.
func (g *graph) ancestorPath(id NodeID) []NodeID {
	path := []NodeID{id}
	current := g.nodes[id].parent
	for current != "" {
		path = append(path, current)
		current = g.nodes[current].parent
	}
	return path
}

// pathLocked returns the root-to-node path of id, both endpoints inclusive.
func (g *graph) pathLocked(id NodeID) []NodeID {
	chain := g.ancestorPath(id)
	slices.Reverse(chain)
	return chain
}

func (g *graph) isDescendantLocked(ancestor, candidate NodeID) bool {
	current := candidate
	for current != "" {
		if current == ancestor {
			return true
		}
		current = g.nodes[current].parent
	}
	return false
}

func (g *graph) removeChildLocked(parent, child NodeID) {
	if parent == "" {
		g.roots = slices.DeleteFunc(g.roots, func(id NodeID) bool { return id == child })
		return
	}
	kids := g.nodes[parent].children
	g.nodes[parent].children = slices.DeleteFunc(kids, func(id NodeID) bool { return id == child })
}

func (g *graph) addChildLocked(parent, child NodeID) {
	if parent == "" {
		g.roots = append(g.roots, child)
		return
	}
	g.nodes[parent].children = append(g.nodes[parent].children, child)
}

// collectActiveKeysLocked gathers every key that can affect a resolution
// anywhere in the forest: keys with explicit values and keys with cut-off
// markers. A key that is only blocked must still be cached as blocked.
func (g *graph) collectActiveKeysLocked() []string {
	seen := make(map[string]struct{})
	for _, n := range g.nodes {
		for key := range n.explicit {
			seen[key] = struct{}{}
		}
		for key := range n.blocked {
			seen[key] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sortStrings(keys)
	return keys
}

func sortStrings(values []string) {
	slices.SortFunc(values, func(a, b string) int { return strings.Compare(a, b) })
}

// activeInheritance is the set of keys that reach a node from its ancestors
// after cut-off markers have been applied. A blocked entry records the node
// that set the nearest cut-off marker.
type activeInheritance map[string]Provenance

// resolveNodeLocked applies n's own explicit tags and cut-off markers to the
// inheritance arriving from above. It returns the cache to store on n (which
// never contains n's own explicit keys) and the active set propagated to n's
// children. An explicit tag overrides a cut-off marker and reopens the key
// for the subtree below.
func resolveNodeLocked(n *node, incoming activeInheritance) (map[string]Provenance, activeInheritance) {
	cache := make(map[string]Provenance)
	outgoing := make(activeInheritance)

	for key, provenance := range incoming {
		cache[key] = provenance
		outgoing[key] = provenance
	}
	for key := range n.explicit {
		provenance := Provenance{Key: key, Source: n.id, Value: n.explicit[key]}
		delete(cache, key)
		outgoing[key] = provenance
	}
	for key := range n.blocked {
		if _, explicit := n.explicit[key]; explicit {
			continue
		}
		provenance := Provenance{Key: key, Source: n.id, Blocked: true}
		provenance.Path = []NodeID{n.id}
		cache[key] = provenance
		outgoing[key] = provenance
	}
	return cache, outgoing
}

// recomputeClosureLocked re-resolves every node in the subtree rooted at root
// for all explicit keys present in the forest. Caches outside the closure are
// left untouched.
func (g *graph) recomputeClosureLocked(root NodeID) {
	keys := g.collectActiveKeysLocked()

	type frame struct {
		id     NodeID
		active activeInheritance
	}

	initial := make(activeInheritance)
	for current := g.nodes[root].parent; current != ""; current = g.nodes[current].parent {
		ancestor := g.nodes[current]
		for _, key := range keys {
			if _, done := initial[key]; done {
				continue
			}
			if value, ok := ancestor.explicit[key]; ok {
				initial[key] = Provenance{Key: key, Source: current, Value: value}
			} else if ancestor.blocked[key] {
				initial[key] = Provenance{Key: key, Source: current, Blocked: true}
			}
		}
	}

	stack := []frame{{id: root, active: initial}}
	for len(stack) > 0 {
		last := len(stack) - 1
		currentFrame := stack[last]
		stack = stack[:last]

		n := g.nodes[currentFrame.id]
		cache, outgoing := resolveNodeLocked(n, currentFrame.active)
		for key, provenance := range cache {
			provenance.Path = g.pathToSourceLocked(currentFrame.id, provenance.Source)
			cache[key] = provenance
		}
		n.inherited = cache

		for _, child := range n.children {
			childActive := make(activeInheritance, len(outgoing))
			for key, provenance := range outgoing {
				provenance.Path = clonePath(provenance.Path)
				childActive[key] = provenance
			}
			stack = append(stack, frame{id: child, active: childActive})
		}
	}
}

// pathToSourceLocked returns the chain from id up to and including source.
func (g *graph) pathToSourceLocked(id, source NodeID) []NodeID {
	chain := []NodeID{id}
	for current := id; current != source; {
		current = g.nodes[current].parent
		if current == "" {
			return chain
		}
		chain = append(chain, current)
	}
	return chain
}

// effectiveLocked returns the effective tags of id sorted by key.
func (g *graph) effectiveLocked(id NodeID) []EffectiveTag {
	n := g.nodes[id]
	keys := make(map[string]struct{}, len(n.explicit)+len(n.inherited))
	for key := range n.explicit {
		keys[key] = struct{}{}
	}
	for key, provenance := range n.inherited {
		if provenance.Blocked {
			continue
		}
		keys[key] = struct{}{}
	}

	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sortStrings(ordered)

	result := make([]EffectiveTag, 0, len(ordered))
	for _, key := range ordered {
		if value, ok := n.explicit[key]; ok {
			result = append(result, EffectiveTag{Key: key, Value: value, Source: id, Explicit: true})
			continue
		}
		provenance := n.inherited[key]
		result = append(result, EffectiveTag{
			Key:    key,
			Value:  provenance.Value,
			Source: provenance.Source,
		})
	}
	return result
}

// inheritedLocked returns a defensive copy of the cached inheritance state.
func (g *graph) inheritedLocked(id NodeID) []Provenance {
	n := g.nodes[id]
	keys := make([]string, 0, len(n.inherited))
	for key := range n.inherited {
		keys = append(keys, key)
	}
	sortStrings(keys)

	result := make([]Provenance, 0, len(keys))
	for _, key := range keys {
		provenance := n.inherited[key]
		provenance.Path = clonePath(provenance.Path)
		result = append(result, provenance)
	}
	return result
}

func (g *graph) explicitLocked(id NodeID) []Tag {
	n := g.nodes[id]
	keys := make([]string, 0, len(n.explicit))
	for key := range n.explicit {
		keys = append(keys, key)
	}
	sortStrings(keys)

	result := make([]Tag, 0, len(keys))
	for _, key := range keys {
		result = append(result, Tag{Key: key, Value: n.explicit[key]})
	}
	return result
}

// verifyProvenanceLocked re-walks the current ancestor chain for one key and
// compares the live resolution against the recorded provenance.
func (g *graph) verifyOneLocked(id NodeID, recorded Provenance) bool {
	n := g.nodes[id]
	if value, explicit := n.explicit[recorded.Key]; explicit {
		return recorded.Blocked == false && recorded.Source == id &&
			recorded.Value == value && sameNodePath(recorded.Path, []NodeID{id})
	}
	if n.blocked[recorded.Key] {
		return recorded.Blocked && recorded.Source == id &&
			recorded.Value == "" && sameNodePath(recorded.Path, []NodeID{id})
	}

	for current := n.parent; current != ""; current = g.nodes[current].parent {
		ancestor := g.nodes[current]
		if value, ok := ancestor.explicit[recorded.Key]; ok {
			expectedPath := g.ancestorPath(id)
			index := slices.Index(expectedPath, current)
			expectedPath = expectedPath[:index+1]
			return !recorded.Blocked && recorded.Source == current &&
				recorded.Value == value && sameNodePath(recorded.Path, expectedPath)
		}
		if ancestor.blocked[recorded.Key] {
			if !recorded.Blocked {
				return false
			}
			expectedPath := g.ancestorPath(id)
			index := slices.Index(expectedPath, current)
			expectedPath = expectedPath[:index+1]
			return recorded.Source == current &&
				recorded.Value == "" && sameNodePath(recorded.Path, expectedPath)
		}
	}

	// No ancestor contributes the key: the cache must not carry it.
	return false
}

func sameNodePath(a, b []NodeID) bool {
	return slices.Equal(a, b)
}

// verifyProvenanceLocked validates every cached inherited entry of id.
func (g *graph) verifyProvenanceLocked(id NodeID) bool {
	n := g.nodes[id]
	for _, recorded := range n.inherited {
		if !g.verifyOneLocked(id, recorded) {
			return false
		}
	}
	return true
}

// cloneLocked returns a deep copy of the graph used for snapshots and
// transaction rollback.
func (g *graph) cloneLocked() *graph {
	copyGraph := &graph{
		nodes: make(map[NodeID]*node, len(g.nodes)),
		roots: clonePath(g.roots),
	}
	for id, n := range g.nodes {
		copyGraph.nodes[id] = &node{
			id:        n.id,
			parent:    n.parent,
			children:  clonePath(n.children),
			explicit:  mapsClone(n.explicit),
			inherited: make(map[string]Provenance, len(n.inherited)),
			blocked:   mapsClone(n.blocked),
		}
		for key, provenance := range n.inherited {
			provenance.Path = clonePath(provenance.Path)
			copyGraph.nodes[id].inherited[key] = provenance
		}
	}
	return copyGraph
}

func mapsClone[V any](source map[string]V) map[string]V {
	if len(source) == 0 {
		return map[string]V{}
	}
	target := make(map[string]V, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}
