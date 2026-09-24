// Package verenum provides versioned enums with stable numeric IDs and
// version-dependent canonical names.
//
// Design properties:
//
//   - Numeric IDs are the serialized identity; names may change across
//     versions but an ID never does.
//   - Old names survive as aliases and still decode; canonical output always
//     uses the active name of the target version.
//   - Within one version a name (active or alias) bound to two different IDs
//     is rejected at registration.
//   - Multi-version graph merges keep alias collisions (migration reports
//     ambiguity) but reject two distinct IDs sharing one active name.
//   - Unknown values pass through lexically; unknown numbers are never
//     coerced into the first declared member.
package verenum

import (
	"fmt"
	"sort"
)

// Version is an ordered schema version (higher == newer).
type Version int

// Rel describes how a chain step relates a name to an ID.
const (
	RelActive = "active" // active (canonical) name of an ID
	RelAlias  = "alias"  // withdrawn/old name pointing at an active name
)

// binding ties one lexical name to a stable ID inside one version graph.
type binding struct {
	id      int64
	active  bool
	version Version
}

// ChainStep is one hop in a rename/conflict chain.
type ChainStep struct {
	Name    string  // lexical name at this hop
	Version Version // version that introduced the binding
	Rel     string  // RelActive or RelAlias
	ID      int64
}

func (s ChainStep) String() string {
	if s.Rel == RelActive {
		return fmt.Sprintf("%q(active id=%d)", s.Name, s.ID)
	}
	return fmt.Sprintf("%q(v%d alias of id=%d)", s.Name, s.Version, s.ID)
}

// graph is one immutable-per-merge version view of a name/ID table.
type graph struct {
	byName map[string][]binding
	canon  map[int64]string
}

func newGraph() *graph {
	return &graph{byName: map[string][]binding{}, canon: map[int64]string{}}
}

func (g *graph) clone() *graph {
	c := newGraph()
	for name, bs := range g.byName {
		c.byName[name] = append([]binding(nil), bs...)
	}
	for id, name := range g.canon {
		c.canon[id] = name
	}
	return c
}

// addActive binds an active name to id. Any pre-existing binding of the same
// name to a different ID is illegal inside one version (conflicting alias
// rejection, "same alias -> two IDs" rejection).
func (g *graph) addActive(v Version, id int64, name string) error {
	if name == "" {
		return fmt.Errorf("verenum: empty active name for id %d", id)
	}
	if existing, ok := g.canon[id]; ok && existing != name {
		return fmt.Errorf("verenum: id %d already has active name %q at v%d, cannot also be %q", id, existing, v, name)
	}
	if err := g.checkConflict(v, name, id); err != nil {
		return err
	}
	g.byName[name] = appendUniqueBinding(g.byName[name], binding{id: id, active: true, version: v})
	g.canon[id] = name
	return nil
}

// addAlias binds an old name to an id. Aliasing the same name to two
// different IDs within one version is rejected.
func (g *graph) addAlias(v Version, id int64, alias string) error {
	if alias == "" {
		return fmt.Errorf("verenum: empty alias for id %d", id)
	}
	if _, ok := g.canon[id]; !ok {
		return fmt.Errorf("verenum: cannot add alias %q for unknown id %d at v%d", alias, id, v)
	}
	if err := g.checkConflict(v, alias, id); err != nil {
		return err
	}
	g.byName[alias] = appendUniqueBinding(g.byName[alias], binding{id: id, active: false, version: v})
	return nil
}

func (g *graph) checkConflict(v Version, name string, id int64) error {
	for _, b := range g.byName[name] {
		if b.id != id {
			return &ConflictError{
				Path: "",
				Name: name,
				Cause: fmt.Sprintf("at v%d name %q is already bound to id %d, cannot bind it to id %d",
					v, name, b.id, id),
			}
		}
	}
	return nil
}

func appendUniqueBinding(bs []binding, b binding) []binding {
	for _, existing := range bs {
		if existing == b {
			return bs
		}
	}
	return append(bs, b)
}

// resolveResult reports what a name resolves to in this graph.
type resolveResult struct {
	id        int64
	found     bool
	ambiguous bool
	bindings  []binding
}

func (g *graph) lookup(name string) resolveResult {
	bs := g.byName[name]
	if len(bs) == 0 {
		return resolveResult{}
	}
	ids := distinctIDs(bs)
	res := resolveResult{found: true, bindings: bs, id: ids[0], ambiguous: len(ids) > 1}
	return res
}

func distinctIDs(bs []binding) []int64 {
	seen := map[int64]bool{}
	var ids []int64
	for _, b := range bs {
		if !seen[b.id] {
			seen[b.id] = true
			ids = append(ids, b.id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// shortestChains computes, per candidate ID, the shortest rename chain from
// name to that ID's active name, using alias -> active-name edges.
func (g *graph) shortestChains(name string) map[int64][]ChainStep {
	ids := distinctIDs(g.byName[name])
	out := make(map[int64][]ChainStep, len(ids))
	for _, id := range ids {
		out[id] = g.bfsChain(name, id)
	}
	return out
}

type bfsNode struct {
	name  string
	chain []ChainStep
}

func (g *graph) bfsChain(start string, targetID int64) []ChainStep {
	target, ok := g.canon[targetID]
	if !ok {
		return nil
	}
	visited := map[string]bool{start: true}
	queue := []bfsNode{{name: start}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.name == target {
			activeVersion := Version(0)
			for _, ab := range g.byName[target] {
				if ab.active && ab.id == targetID {
					activeVersion = ab.version
					break
				}
			}
			chain := append([]ChainStep(nil), cur.chain...)
			return append(chain, ChainStep{Name: target, Version: activeVersion, Rel: RelActive, ID: targetID})
		}
		// Deterministic expansion order.
		nexts := g.byName[cur.name]
		sorted := append([]binding(nil), nexts...)
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].version != sorted[j].version {
				return sorted[i].version < sorted[j].version
			}
			return sorted[i].id < sorted[j].id
		})
		for _, b := range sorted {
			// Active bindings are terminals, not edges: a rename is already
			// represented in the merged graph by a synthesized alias binding
			// at the version where the new active name appeared.
			if b.active {
				continue
			}
			next := g.canon[b.id]
			if next == "" || visited[next] {
				continue
			}
			visited[next] = true
			step := ChainStep{Name: cur.name, Version: b.version, Rel: RelAlias, ID: b.id}
			chain := make([]ChainStep, len(cur.chain)+1)
			copy(chain, cur.chain)
			chain[len(cur.chain)] = step
			queue = append(queue, bfsNode{name: next, chain: chain})
		}
	}
	return nil
}
