// Package enumkit implements versioned enums with stable numeric IDs,
// per-version names, old-name aliases, unknown-value passthrough and
// safe serialization. It uses only the Go standard library.
package enumkit

import (
	"fmt"
	"sort"
	"strconv"
)

// Kind describes how a Value was resolved against a registry.
type Kind int

const (
	// KnownName is a value resolved through a name or alias.
	KnownName Kind = iota
	// KnownNumber is a value resolved through a numeric literal.
	KnownNumber
	// UnknownName is an unrecognized name, passed through lexically.
	UnknownName
	// UnknownNumber is an unrecognized numeric ID, passed through
	// lexically. It is never coerced to any known member.
	UnknownNumber
)

// Value is a decoded enum token. Raw always preserves the original
// lexical token so unknown values round-trip unchanged.
type Value struct {
	Kind Kind
	ID   int64  // stable ID; meaningful only when Known() is true
	Raw  string // original lexical token, always preserved
}

// Known reports whether the value resolved to a defined member.
func (v Value) Known() bool { return v.Kind == KnownName || v.Kind == KnownNumber }

// Alias binds an old/extra name to a stable ID within one version.
type Alias struct {
	Name string
	ID   int64
}

// Version describes the naming of an enum at one revision.
// Active maps stable IDs to their active (canonical-at-version) names.
// Aliases carries old names still accepted at this version.
type Version struct {
	Name    string
	Active  map[int64]string
	Aliases []Alias
}

// version is the validated runtime form of Version.
type version struct {
	name    string
	active  map[int64]string
	aliases []Alias
	resolve map[string]int64 // every accepted name -> stable ID
}

// Registry is one versioned enum.
type Registry struct {
	name     string
	canon    map[int64]string // stable ID -> current canonical name
	byName   map[string]int64 // current canonical name -> stable ID
	versions map[string]*version
	order    []string
}

// New creates an empty registry for an enum with the given name.
func New(name string) *Registry {
	return &Registry{
		name:     name,
		canon:    map[int64]string{},
		byName:   map[string]int64{},
		versions: map[string]*version{},
	}
}

// Name returns the enum name.
func (r *Registry) Name() string { return r.name }

// Define adds a member with a stable ID and its current canonical name.
// Renames later do not change the ID, so serialization identity is kept.
func (r *Registry) Define(id int64, name string) error {
	if id < 0 || id > MaxID {
		return fmt.Errorf("enum %s: id %d out of supported range [0,%d]", r.name, id, MaxID)
	}
	if len(name) == 0 || len(name) > MaxTokenLen {
		return fmt.Errorf("enum %s: name %q empty or longer than %d bytes", r.name, name, MaxTokenLen)
	}
	if _, dup := r.canon[id]; dup {
		return fmt.Errorf("enum %s: id %d already defined", r.name, id)
	}
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("enum %s: name %q already defined", r.name, name)
	}
	r.canon[id] = name
	r.byName[name] = id
	return nil
}

// AddVersion validates and registers a naming revision.
// Within one version a name must map to exactly one stable ID:
// an alias bound to two different IDs (directly, or by colliding with
// an active name of another member) is rejected as illegal.
func (r *Registry) AddVersion(v Version) error {
	if v.Name == "" {
		return fmt.Errorf("enum %s: version name must not be empty", r.name)
	}
	if _, dup := r.versions[v.Name]; dup {
		return fmt.Errorf("enum %s: version %q already registered", r.name, v.Name)
	}
	rv := &version{
		name:    v.Name,
		active:  map[int64]string{},
		resolve: map[string]int64{},
	}
	for id, name := range v.Active {
		if _, ok := r.canon[id]; !ok {
			return fmt.Errorf("enum %s: version %q: active name %q for undefined id %d", r.name, v.Name, name, id)
		}
		if prev, dup := rv.resolve[name]; dup && prev != id {
			return &ConflictError{Enum: r.name, Version: v.Name, Name: name, IDs: []int64{prev, id}}
		}
		rv.active[id] = name
		rv.resolve[name] = id
	}
	for _, a := range v.Aliases {
		if _, ok := r.canon[a.ID]; !ok {
			return fmt.Errorf("enum %s: version %q: alias %q for undefined id %d", r.name, v.Name, a.Name, a.ID)
		}
		if prev, dup := rv.resolve[a.Name]; dup {
			if prev != a.ID {
				return &ConflictError{Enum: r.name, Version: v.Name, Name: a.Name, IDs: []int64{prev, a.ID}}
			}
			continue // same alias to the same id: idempotent
		}
		rv.aliases = append(rv.aliases, a)
		rv.resolve[a.Name] = a.ID
	}
	r.versions[v.Name] = rv
	r.order = append(r.order, v.Name)
	return nil
}

// RetractAlias removes an alias from a version. Snapshots taken before
// the retraction are unaffected because they hold frozen copies.
func (r *Registry) RetractAlias(ver, name string) error {
	rv, ok := r.versions[ver]
	if !ok {
		return fmt.Errorf("enum %s: unknown version %q", r.name, ver)
	}
	if _, isActive := rv.resolve[name]; isActive {
		for _, n := range rv.active {
			if n == name {
				return fmt.Errorf("enum %s: version %q: %q is an active name, not an alias", r.name, ver, name)
			}
		}
	}
	kept := rv.aliases[:0]
	found := false
	for _, a := range rv.aliases {
		if a.Name == name {
			found = true
			continue
		}
		kept = append(kept, a)
	}
	if !found {
		return fmt.Errorf("enum %s: version %q: no alias %q", r.name, ver, name)
	}
	rv.aliases = kept
	delete(rv.resolve, name)
	return nil
}

// Versions returns registered version names in registration order.
func (r *Registry) Versions() []string { return append([]string(nil), r.order...) }

// Decode resolves a lexical token at a given version. Unknown names and
// unknown numeric IDs pass through lexically; an unknown number is never
// coerced to the first (or any) member.
func (r *Registry) Decode(ver, token string) (Value, error) {
	rv, ok := r.versions[ver]
	if !ok {
		return Value{}, fmt.Errorf("enum %s: unknown version %q", r.name, ver)
	}
	if id, ok := rv.resolve[token]; ok {
		return Value{Kind: KnownName, ID: id, Raw: token}, nil
	}
	if n, err := strconv.ParseInt(token, 10, 64); err == nil {
		if _, ok := r.canon[n]; ok {
			return Value{Kind: KnownNumber, ID: n, Raw: token}, nil
		}
		return Value{Kind: UnknownNumber, ID: n, Raw: token}, nil
	}
	return Value{Kind: UnknownName, Raw: token}, nil
}

// CanonicalName returns the current canonical name for a stable ID.
func (r *Registry) CanonicalName(id int64) (string, bool) {
	n, ok := r.canon[id]
	return n, ok
}

// NameAt returns the active name of an ID at a specific version.
func (r *Registry) NameAt(ver string, id int64) (string, bool) {
	rv, ok := r.versions[ver]
	if !ok {
		return "", false
	}
	n, ok := rv.active[id]
	return n, ok
}

// Canonical renders a value using the current canonical name; unknown
// values keep their original lexical token (passthrough).
func (r *Registry) Canonical(v Value) string {
	if v.Known() {
		if name, ok := r.canon[v.ID]; ok {
			return name
		}
	}
	return v.Raw
}

// IDs returns all defined stable IDs in ascending order.
func (r *Registry) IDs() []int64 {
	ids := make([]int64, 0, len(r.canon))
	for id := range r.canon {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Snapshot is a frozen copy of one version's naming. Later alias
// retractions or new versions never mutate it.
type Snapshot struct {
	enum    string
	version string
	resolve map[string]int64
	active  map[int64]string
}

// Snapshot freezes a version's name mapping.
func (r *Registry) Snapshot(ver string) (*Snapshot, error) {
	rv, ok := r.versions[ver]
	if !ok {
		return nil, fmt.Errorf("enum %s: unknown version %q", r.name, ver)
	}
	s := &Snapshot{
		enum:    r.name,
		version: ver,
		resolve: make(map[string]int64, len(rv.resolve)),
		active:  make(map[int64]string, len(rv.active)),
	}
	for k, v := range rv.resolve {
		s.resolve[k] = v
	}
	for k, v := range rv.active {
		s.active[k] = v
	}
	return s, nil
}

// Resolve looks up a name in the frozen snapshot.
func (s *Snapshot) Resolve(name string) (int64, bool) {
	id, ok := s.resolve[name]
	return id, ok
}

// ActiveName returns the frozen active name for an ID.
func (s *Snapshot) ActiveName(id int64) (string, bool) {
	n, ok := s.active[id]
	return n, ok
}

// Rename updates the current canonical name of an existing member.
// Its stable ID (and therefore serialized identity) is unchanged.
func (r *Registry) Rename(id int64, name string) error {
	old, ok := r.canon[id]
	if !ok {
		return fmt.Errorf("enum %s: cannot rename undefined id %d", r.name, id)
	}
	if old == name {
		return nil
	}
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("enum %s: name %q already in use", r.name, name)
	}
	delete(r.byName, old)
	r.canon[id] = name
	r.byName[name] = id
	return nil
}
