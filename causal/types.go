// Package causal implements causal-version bookkeeping and the selection of a
// causally consistent slice over a multi-object replicated store.
//
// Every object version carries an explicit causal label: the list of versions
// on which it causally depends (its immediate predecessors). A slice is
// consistent iff it is closed under these dependencies: no version appears
// without every version that produced it.
package causal

import (
	"fmt"
	"sort"
	"strings"
)

// Ref identifies one version of one object.
type Ref struct {
	Object string
	Seq    int // per-object sequence number, starting at 1
}

func (r Ref) String() string { return fmt.Sprintf("%s@%d", r.Object, r.Seq) }

// Version is one immutable object version and its immediate causal deps.
// Deps lists the versions that must be present whenever this version is
// present. The predecessor on the same object is included explicitly so that
// the dependency relation fully captures per-object prefix closure as well.
type Version struct {
	Ref  Ref
	Deps []Ref
}

// Registry is an append-only, globally shared table of stamped versions.
// It is written during version creation and only read afterwards, which is
// safe for the single-threaded event simulator.
type Registry struct {
	versions map[Ref]Version
	maxSeq   map[string]int
}

func NewRegistry() *Registry {
	return &Registry{versions: map[Ref]Version{}, maxSeq: map[string]int{}}
}

// Stamp creates version (object, seq) with the given causal dependencies.
// seq must be exactly one greater than the object's previous version; every
// dependency must already be registered.
func (r *Registry) Stamp(object string, deps []Ref) (Ref, error) {
	seq := r.maxSeq[object] + 1
	ref := Ref{Object: object, Seq: seq}
	seen := map[Ref]bool{}
	clean := make([]Ref, 0, len(deps))
	for _, d := range deps {
		if _, ok := r.versions[d]; !ok {
			return Ref{}, fmt.Errorf("stamp %s: unknown dependency %s", ref, d)
		}
		if d.Object == object {
			return Ref{}, fmt.Errorf("stamp %s: same-object predecessor must not be passed explicitly", ref)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		clean = append(clean, d)
	}
	// Prefix closure on the same object is represented explicitly.
	if seq > 1 {
		clean = append(clean, Ref{Object: object, Seq: seq - 1})
	}
	sort.Slice(clean, func(i, j int) bool {
		if clean[i].Object != clean[j].Object {
			return clean[i].Object < clean[j].Object
		}
		return clean[i].Seq < clean[j].Seq
	})
	r.versions[ref] = Version{Ref: ref, Deps: clean}
	r.maxSeq[object] = seq
	return ref, nil
}

func (r *Registry) Lookup(ref Ref) (Version, bool) {
	v, ok := r.versions[ref]
	return v, ok
}

func (r *Registry) MaxSeq(object string) int { return r.maxSeq[object] }

func (r *Registry) Objects() []string {
	out := make([]string, 0, len(r.maxSeq))
	for o := range r.maxSeq {
		out = append(out, o)
	}
	sort.Strings(out)
	return out
}

// VV is a version vector: for every named object it names the newest selected
// version. Because dependency labels include the same-object predecessor, a
// version vector stands for the whole per-object prefix {1..vv[o]}.
type VV map[string]int

func (v VV) clone() VV {
	c := make(VV, len(v))
	for o, n := range v {
		c[o] = n
	}
	return c
}

// merge raises every component of v to at least the value in other.
func (v VV) merge(other VV) {
	for o, n := range other {
		if n > v[o] {
			v[o] = n
		}
	}
}

// dominatedBy reports whether v <= other component-wise.
func (v VV) dominatedBy(other VV) bool {
	for o, n := range v {
		if n > other[o] {
			return false
		}
	}
	return true
}

func (v VV) String() string {
	keys := make([]string, 0, len(v))
	for o := range v {
		keys = append(keys, o)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, o := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", o, v[o]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}
