// Package merge layers deployment configuration maps.
//
// Layers are applied in order with these semantics:
//   - objects (map[string]interface{}) merge recursively;
//   - arrays and scalars replace any previous value wholesale;
//   - the Delete sentinel removes the key (and its subtree);
//   - a nil value keeps the key with an explicit null.
//
// The merge is computed over a path-flattened model: every layer is
// expanded into dot-joined path -> leaf entries, later layers override
// earlier ones per path, and the result is unflattened back into a
// nested map. Provenance tracks, for every final leaf, the layer that
// supplied it and the chain of values it overrode.
package merge

import (
	"sort"
	"strings"
)

// deleteMarker is the type of the Delete sentinel.
type deleteMarker struct{}

// Delete is a sentinel layer value that removes the key (and its whole
// subtree) from the merged configuration. Unlike nil, which keeps the
// key with an explicit null, Delete drops the key entirely.
var Delete = deleteMarker{}

// IsDelete reports whether v is the Delete sentinel.
func IsDelete(v interface{}) bool {
	_, ok := v.(deleteMarker)
	return ok
}

// Change records one value that held a path at a given layer.
type Change struct {
	Layer int
	Value interface{}
}

// Leaf is a final flattened configuration entry with its provenance.
type Leaf struct {
	Path    string
	Value   interface{}
	Layer   int      // layer that supplied the final value
	History []Change // overridden chain, oldest first
}

// Op identifies why a flattened entry was removed.
type Op int

const (
	// OpDelete: the key (or an ancestor of it) was explicitly deleted.
	OpDelete Op = iota
	// OpDroppedByParent: an ancestor was replaced by a leaf
	// (scalar, array or null), discarding this subtree.
	OpDroppedByParent
	// OpReplacedByChild: a leaf was replaced because a layer created
	// an object below it.
	OpReplacedByChild
)

// Removal archives one removed entry so Explain can account for paths
// that do not exist in the final configuration.
type Removal struct {
	Op          Op
	Layer       int         // layer that triggered the removal
	Cause       string      // ancestor path responsible, if any
	Value       interface{} // value that was removed
	SourceLayer int         // layer that had supplied Value
}

// flatEntry is a live entry in the flattened merge state.
type flatEntry struct {
	value   interface{}
	layer   int
	history []Change
}

// Result is the outcome of Merge: the merged configuration plus
// per-path provenance and a removal archive.
type Result struct {
	Merged  map[string]interface{}
	leaves  map[string]*flatEntry
	removed map[string][]Removal
}

// Merge applies layers in order and returns the merged configuration
// with full provenance.
func Merge(layers ...map[string]interface{}) *Result {
	r := &Result{
		leaves:  map[string]*flatEntry{},
		removed: map[string][]Removal{},
	}
	for i, layer := range layers {
		r.apply(i, flatten(layer))
	}
	r.Merged = unflatten(r.leaves)
	return r
}

// apply folds one flattened layer into the merge state.
func (r *Result) apply(layer int, flat map[string]interface{}) {
	paths := make([]string, 0, len(flat))
	for p := range flat {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		val := flat[path]
		if IsDelete(val) {
			r.remove(layer, path, OpDelete, path)
			continue
		}
		// A leaf at path discards any existing subtree below it.
		for _, p := range r.descendants(path) {
			r.removeEntry(p, Removal{Op: OpDroppedByParent, Layer: layer, Cause: path})
		}
		// A leaf at path also replaces any ancestor leaf: setting
		// "a.b" turns a scalar "a" into an object.
		for _, anc := range ancestors(path) {
			if _, ok := r.leaves[anc]; ok {
				r.removeEntry(anc, Removal{Op: OpReplacedByChild, Layer: layer, Cause: path})
			}
		}
		if e, ok := r.leaves[path]; ok {
			e.history = append(e.history, Change{Layer: e.layer, Value: e.value})
			e.value = val
			e.layer = layer
		} else {
			r.leaves[path] = &flatEntry{value: val, layer: layer}
		}
	}
}

// remove deletes the entry at path and its whole subtree, archiving
// each removed entry with the given op and cause.
func (r *Result) remove(layer int, path string, op Op, cause string) {
	if _, ok := r.leaves[path]; ok {
		r.removeEntry(path, Removal{Op: op, Layer: layer, Cause: cause})
	}
	for _, p := range r.descendants(path) {
		r.removeEntry(p, Removal{Op: op, Layer: layer, Cause: cause})
	}
}

// removeEntry deletes one flattened entry and archives the removal.
// Removing a path that does not exist is a no-op, so repeated deletes
// are harmless.
func (r *Result) removeEntry(path string, rem Removal) {
	e, ok := r.leaves[path]
	if !ok {
		return
	}
	rem.Value = e.value
	rem.SourceLayer = e.layer
	r.removed[path] = append(r.removed[path], rem)
	delete(r.leaves, path)
}

// descendants returns the sorted paths of all live entries strictly
// below path.
func (r *Result) descendants(path string) []string {
	prefix := path + "."
	var out []string
	for p := range r.leaves {
		if strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// ancestors lists the enclosing paths of path, nearest first:
// ancestors("a.b.c") = ["a.b", "a"].
func ancestors(path string) []string {
	segs := strings.Split(path, ".")
	out := make([]string, 0, len(segs)-1)
	for i := len(segs) - 1; i > 0; i-- {
		out = append(out, strings.Join(segs[:i], "."))
	}
	return out
}

// flatten expands a layer into path -> leaf entries. Objects recurse;
// arrays, scalars, nil and Delete are leaves.
func flatten(layer map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	var walk func(prefix string, m map[string]interface{})
	walk = func(prefix string, m map[string]interface{}) {
		for k, v := range m {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			if sub, ok := v.(map[string]interface{}); ok {
				walk(p, sub)
				continue
			}
			out[p] = v
		}
	}
	walk("", layer)
	return out
}

// unflatten rebuilds the nested configuration from the live entries.
func unflatten(leaves map[string]*flatEntry) map[string]interface{} {
	root := map[string]interface{}{}
	paths := make([]string, 0, len(leaves))
	for p := range leaves {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		segs := strings.Split(p, ".")
		node := root
		for _, s := range segs[:len(segs)-1] {
			next, ok := node[s].(map[string]interface{})
			if !ok {
				next = map[string]interface{}{}
				node[s] = next
			}
			node = next
		}
		node[segs[len(segs)-1]] = leaves[p].value
	}
	return root
}

// Leaf returns the final leaf at path and whether it exists.
func (r *Result) Leaf(path string) (Leaf, bool) {
	e, ok := r.leaves[path]
	if !ok {
		return Leaf{}, false
	}
	return Leaf{Path: path, Value: e.value, Layer: e.layer, History: e.history}, true
}

// Leaves returns every final leaf, sorted by path.
func (r *Result) Leaves() []Leaf {
	paths := make([]string, 0, len(r.leaves))
	for p := range r.leaves {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]Leaf, 0, len(paths))
	for _, p := range paths {
		l, _ := r.Leaf(p)
		out = append(out, l)
	}
	return out
}

// Removals returns the archived removals for path, oldest first.
func (r *Result) Removals(path string) []Removal {
	return r.removed[path]
}
