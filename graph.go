package enumkit

import (
	"fmt"
	"sort"
	"strconv"
)

// edge is one name<->ID binding contributed by a version.
type edge struct {
	name    string
	id      int64
	version string
	active  bool
}

// Graph is the merged alias graph of one or more versions.
type Graph struct {
	reg      *Registry
	versions []string
	byName   map[string][]edge
	byID     map[int64][]edge
}

// Merge builds the merged alias graph of the given versions (all known
// versions in registration order when none are listed).
//
// Merging is allowed, but if two different stable IDs end up mapped to
// the same active name a MergeConflictError carrying the shortest
// conflict chain is returned. Alias-only name collisions across
// versions are retained; migrating through them yields AmbiguityError.
func (r *Registry) Merge(versions ...string) (*Graph, error) {
	if len(versions) == 0 {
		versions = r.Versions()
	}
	g := &Graph{
		reg:      r,
		versions: append([]string(nil), versions...),
		byName:   map[string][]edge{},
		byID:     map[int64][]edge{},
	}
	for _, vn := range versions {
		rv, ok := r.versions[vn]
		if !ok {
			return nil, fmt.Errorf("enum %s: unknown version %q", r.name, vn)
		}
		for id, name := range rv.active {
			e := edge{name: name, id: id, version: vn, active: true}
			g.byName[name] = append(g.byName[name], e)
			g.byID[id] = append(g.byID[id], e)
		}
		for _, a := range rv.aliases {
			e := edge{name: a.Name, id: a.ID, version: vn, active: false}
			g.byName[a.Name] = append(g.byName[a.Name], e)
			g.byID[a.ID] = append(g.byID[a.ID], e)
		}
	}
	for name, edges := range g.byName {
		ids := map[int64]bool{}
		hasActive := false
		for _, e := range edges {
			ids[e.id] = true
			if e.active {
				hasActive = true
			}
		}
		if hasActive && len(ids) > 1 {
			list := sortedIDs(ids)
			return nil, &MergeConflictError{
				Enum:  r.name,
				Name:  name,
				IDs:   list,
				Chain: g.chain(list[0], list[1]),
			}
		}
	}
	return g, nil
}

// shortestChain BFS over ID nodes, moving between IDs that share a
// name. Returns the shortest hop sequence from fromID to toID.
func (g *Graph) chain(fromID, toID int64) []ChainStep {
	if fromID == toID {
		return nil
	}
	type node struct {
		id   int64
		path []ChainStep
	}
	seen := map[int64]bool{fromID: true}
	queue := []node{{id: fromID}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e1 := range g.byID[cur.id] {
			for _, e2 := range g.byName[e1.name] {
				if e2.id == cur.id || seen[e2.id] {
					continue
				}
				step := ChainStep{
					FromID:      cur.id,
					Name:        e1.name,
					ToID:        e2.id,
					FromVersion: e1.version,
					ToVersion:   e2.version,
					FromActive:  e1.active,
					ToActive:    e2.active,
				}
				path := append(append([]ChainStep{}, cur.path...), step)
				if e2.id == toID {
					return path
				}
				seen[e2.id] = true
				queue = append(queue, node{id: e2.id, path: path})
			}
		}
	}
	return nil
}

func sortedIDs(set map[int64]bool) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Migrate resolves token (encoded at from) through the merged graph and
// returns its stable identity for to. Unknown values pass through
// lexically. If the token is ambiguous after merging, an
// AmbiguityError with candidates and the shortest conflict chain is
// returned instead of an arbitrary choice.
func (g *Graph) Migrate(from, to, token string) (Value, error) {
	if _, ok := g.reg.versions[from]; !ok {
		return Value{}, fmt.Errorf("enum %s: unknown version %q", g.reg.name, from)
	}
	if _, ok := g.reg.versions[to]; !ok {
		return Value{}, fmt.Errorf("enum %s: unknown version %q", g.reg.name, to)
	}
	if n, err := strconv.ParseInt(token, 10, 64); err == nil {
		if _, ok := g.reg.canon[n]; ok {
			return Value{Kind: KnownNumber, ID: n, Raw: token}, nil
		}
		return Value{Kind: UnknownNumber, ID: n, Raw: token}, nil
	}
	set := map[int64]bool{}
	for _, e := range g.byName[token] {
		set[e.id] = true
	}
	ids := sortedIDs(set)
	switch len(ids) {
	case 0:
		return Value{Kind: UnknownName, Raw: token}, nil
	case 1:
		return Value{Kind: KnownName, ID: ids[0], Raw: token}, nil
	default:
		return Value{}, &AmbiguityError{
			Enum:  g.reg.name,
			Token: token,
			IDs:   ids,
			Chain: g.chain(ids[0], ids[1]),
		}
	}
}

// CanonicalAt renders a migrated value using the active name at target
// version; unknown values keep their original lexical token.
func (g *Graph) CanonicalAt(to string, v Value) string {
	if v.Known() {
		if name, ok := g.reg.NameAt(to, v.ID); ok {
			return name
		}
		if name, ok := g.reg.CanonicalName(v.ID); ok {
			return name
		}
	}
	return v.Raw
}
