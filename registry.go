package verenum

import (
	"fmt"
	"sort"
)

// Registry holds enum graphs for every released schema version.
type Registry struct {
	versions map[Version]*graph
}

func New() *Registry {
	return &Registry{versions: map[Version]*graph{}}
}

// Register declares id in version v with an active canonical name and zero or
// more old-name aliases. It is rejected when any of the names is already
// bound to a different ID within that version.
func (r *Registry) Register(v Version, id int64, activeName string, aliases ...string) error {
	g := r.graph(v)
	if err := g.addActive(v, id, activeName); err != nil {
		return err
	}
	for _, alias := range aliases {
		if err := g.addAlias(v, id, alias); err != nil {
			return err
		}
	}
	return nil
}

// MustRegister panics on registration failure; intended for package-level
// setup of compile-time-known schemas.
func (r *Registry) MustRegister(v Version, id int64, activeName string, aliases ...string) {
	if err := r.Register(v, id, activeName, aliases...); err != nil {
		panic(err)
	}
}

// WithdrawAlias removes an alias from one version's live graph. Snapshots
// already taken keep the alias, so historical documents stay decodable.
func (r *Registry) WithdrawAlias(v Version, alias string) bool {
	g, ok := r.versions[v]
	if !ok {
		return false
	}
	if _, exists := g.byName[alias]; !exists {
		return false
	}
	delete(g.byName, alias)
	return true
}

// Lookup resolves a name inside a single version.
func (r *Registry) Lookup(v Version, name string) (id int64, ok bool) {
	g, exists := r.versions[v]
	if !exists {
		return 0, false
	}
	res := g.lookup(name)
	return res.id, res.found && !res.ambiguous
}

// Canonical returns the active name of id at version v.
func (r *Registry) Canonical(v Version, id int64) (string, bool) {
	g, ok := r.versions[v]
	if !ok {
		return "", false
	}
	name, ok := g.canon[id]
	return name, ok
}

// Snapshot freezes one version's name/ID table. Later registration changes
// or alias withdrawals on the registry never affect the snapshot.
func (r *Registry) Snapshot(v Version) (*Snapshot, bool) {
	g, ok := r.versions[v]
	if !ok {
		return nil, false
	}
	return &Snapshot{version: v, graph: g.clone()}, true
}

// Snapshot is an immutable historical schema view.
type Snapshot struct {
	version Version
	graph   *graph
}

func (s *Snapshot) Version() Version { return s.version }
func (s *Snapshot) Lookup(name string) (int64, bool) {
	res := s.graph.lookup(name)
	return res.id, res.found && !res.ambiguous
}
func (s *Snapshot) Canonical(id int64) (string, bool) {
	name, ok := s.graph.canon[id]
	return name, ok
}

func (r *Registry) graph(v Version) *graph {
	g, ok := r.versions[v]
	if !ok {
		g = newGraph()
		r.versions[v] = g
	}
	return g
}

func (r *Registry) hasVersion(v Version) bool {
	_, ok := r.versions[v]
	return ok
}

// sortedVersions returns all registered versions in ascending order.
func (r *Registry) sortedVersions() []Version {
	out := make([]Version, 0, len(r.versions))
	for v := range r.versions {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// versionSpan returns every registered version within [from,to] ascending.
func (r *Registry) versionSpan(from, to Version) []Version {
	if from > to {
		from, to = to, from
	}
	var out []Version
	for _, v := range r.sortedVersions() {
		if v >= from && v <= to {
			out = append(out, v)
		}
	}
	return out
}

// mergeGraphs unions graphs. Two different stable IDs sharing one active name
// is rejected. Diverging aliases are retained: migration then reports
// ambiguity instead of silently choosing a binding.
func (r *Registry) mergeGraphs(versions []Version) (*graph, error) {
	merged := newGraph()
	for _, v := range versions {
		g, ok := r.versions[v]
		if !ok {
			return nil, fmt.Errorf("verenum: unknown version v%d", v)
		}
		for name, bs := range g.byName {
			for _, b := range bs {
				if b.active {
					for _, existing := range merged.byName[name] {
						if existing.active && existing.id != b.id {
							return nil, &MergeError{Name: name, Bindings: []ChainStep{
								{Name: name, Version: existing.version, Rel: RelActive, ID: existing.id},
								{Name: name, Version: b.version, Rel: RelActive, ID: b.id},
							}}
						}
					}
				}
				merged.byName[name] = appendUniqueBinding(merged.byName[name], b)
				if b.active {
					// Renaming one ID across versions is legal: synthesize an
					// alias edge from the previous active name at this version.
					if prev, taken := merged.canon[b.id]; taken && prev != name {
						merged.byName[prev] = appendUniqueBinding(
							merged.byName[prev],
							binding{id: b.id, active: false, version: v},
						)
					}
					merged.canon[b.id] = name
				}
			}
		}
	}
	return merged, nil
}
