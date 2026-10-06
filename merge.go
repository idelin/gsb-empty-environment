// Package cfgmerge merges layered deployment configurations and tracks
// where every final leaf value came from.
//
// Layers are applied in order; later layers win. The merge rules are:
//
//   - objects merge recursively, key by key
//   - arrays replace wholesale (an array is an opaque leaf)
//   - scalars and nulls replace whatever was there; null keeps the key
//   - the Delete sentinel removes the key and anything below it
//   - a type switch replaces: a leaf set over an object discards the
//     subtree, and an object merged over a leaf discards the leaf
//
// Internally the merged configuration is modelled as a flat set of
// dot-separated leaf paths (scalars, arrays and nulls are leaves), which
// is also the model the tests assert against. Keys must not contain ".".
package cfgmerge

import (
	"fmt"
	"sort"
	"strings"
)

// Delete is a sentinel value for explicit key removal. Use it as a value
// in a layer to remove the key (and its subtree) from the merged result.
// Unlike Delete, a nil value is a real value: it keeps the key.
//
// Delete inside an array is not interpreted; arrays are opaque leaves.
var Delete = deleteMarker{}

type deleteMarker struct{}

func isDelete(v any) bool {
	_, ok := v.(deleteMarker)
	return ok
}

// entry is one leaf in the merged result.
type entry struct {
	value any
	setBy int // layer that set the current value
}

type eventKind int

const (
	evSet        eventKind = iota // leaf set at path
	evDelete                      // explicit delete matched something at path
	evDeleteNoop                  // explicit delete matched nothing
	evRemoved                     // path removed as a side effect of another change
)

type removeCause int

const (
	causeDelete           removeCause = iota // removed by a delete at an ancestor
	causeReplacedByLeaf                      // ancestor was replaced by a leaf value
	causeReplacedByObject                    // leaf was replaced when an object grew over it
)

type event struct {
	layer int
	kind  eventKind
	path  string      // affected path
	by    string      // evRemoved only: path whose change caused the removal
	cause removeCause // evRemoved only
}

// Merger accumulates layers and records provenance.
type Merger struct {
	layers int
	flat   map[string]entry
	events []event
}

// NewMerger returns an empty Merger.
func NewMerger() *Merger {
	return &Merger{flat: map[string]entry{}}
}

// Layers returns how many layers have been applied.
func (m *Merger) Layers() int { return m.layers }

// Apply merges one layer on top of the current state.
func (m *Merger) Apply(layer map[string]any) {
	li := m.layers
	m.layers++
	flat := flattenLayer(layer)
	for _, p := range sortedKeys(flat) {
		v := flat[p]
		if isDelete(v) {
			m.applyDelete(p, li)
		} else {
			m.applySet(p, v, li)
		}
	}
}

// applySet records a leaf value at path. A leaf replaces the subtree below
// it, and an object growing over an ancestor leaf replaces that leaf.
func (m *Merger) applySet(path string, v any, layer int) {
	for _, k := range m.keysUnder(path) {
		delete(m.flat, k)
		m.events = append(m.events, event{layer: layer, kind: evRemoved, path: k, by: path, cause: causeReplacedByLeaf})
	}
	for _, a := range ancestors(path) {
		if _, ok := m.flat[a]; ok {
			delete(m.flat, a)
			m.events = append(m.events, event{layer: layer, kind: evRemoved, path: a, by: path, cause: causeReplacedByObject})
		}
	}
	m.flat[path] = entry{value: v, setBy: layer}
	m.events = append(m.events, event{layer: layer, kind: evSet, path: path})
}

// applyDelete removes the leaf at path and everything below it. Deleting a
// path that matches nothing is a no-op, recorded for explanations.
func (m *Merger) applyDelete(path string, layer int) {
	_, exact := m.flat[path]
	under := m.keysUnder(path)
	if !exact && len(under) == 0 {
		m.events = append(m.events, event{layer: layer, kind: evDeleteNoop, path: path})
		return
	}
	if exact {
		delete(m.flat, path)
	}
	for _, k := range under {
		delete(m.flat, k)
		m.events = append(m.events, event{layer: layer, kind: evRemoved, path: k, by: path, cause: causeDelete})
	}
	m.events = append(m.events, event{layer: layer, kind: evDelete, path: path})
}

// keysUnder returns sorted leaf paths strictly below path.
func (m *Merger) keysUnder(path string) []string {
	prefix := path + "."
	var out []string
	for k := range m.flat {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// ancestors returns the enclosing paths of p, outermost first:
// ancestors("a.b.c") = ["a", "a.b"].
func ancestors(p string) []string {
	parts := strings.Split(p, ".")
	out := make([]string, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "."))
	}
	return out
}

// Lookup returns the final leaf value at path.
func (m *Merger) Lookup(path string) (any, bool) {
	e, ok := m.flat[path]
	return e.value, ok
}

// Source returns the layer that set the final leaf value at path.
func (m *Merger) Source(path string) (int, bool) {
	e, ok := m.flat[path]
	return e.setBy, ok
}

// Overridden returns the layers that previously set a leaf at path and were
// later overridden, oldest first. It is empty for unknown paths.
func (m *Merger) Overridden(path string) []int {
	sets := setLayers(m.events, path)
	if _, ok := m.flat[path]; ok && len(sets) > 0 {
		sets = sets[:len(sets)-1] // drop the final, winning set
	}
	if len(sets) == 0 {
		return nil
	}
	return sets
}

// Flatten returns the merged configuration as leaf path -> value.
func (m *Merger) Flatten() map[string]any {
	out := make(map[string]any, len(m.flat))
	for p, e := range m.flat {
		out[p] = e.value
	}
	return out
}

// Map returns the merged configuration as nested maps.
func (m *Merger) Map() map[string]any {
	root := map[string]any{}
	for _, p := range m.sortedPaths() {
		setNested(root, strings.Split(p, "."), m.flat[p].value)
	}
	return root
}

func (m *Merger) sortedPaths() []string {
	out := make([]string, 0, len(m.flat))
	for p := range m.flat {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Explain describes the fate of path: its source and overridden chain if it
// exists, or why it does not (never set, deleted, removed by a parent
// replacement, or present only as an object).
func (m *Merger) Explain(path string) string {
	if e, ok := m.flat[path]; ok {
		msg := fmt.Sprintf("%s = %s, set by layer %d", path, render(e.value), e.setBy)
		if prev := m.Overridden(path); len(prev) > 0 {
			msg += fmt.Sprintf("; overrode layer(s) %s", joinInts(prev))
		}
		if dels := m.deleteLayers(path); len(dels) > 0 {
			msg += fmt.Sprintf("; deleted at layer(s) %s before being re-set", joinInts(dels))
		}
		return msg
	}
	if children := m.keysUnder(path); len(children) > 0 {
		msg := fmt.Sprintf("%s is an object with leaf key(s) %s", path, strings.Join(children, ", "))
		if ev, ok := lastEvent(m.events, path); ok && ev.kind == evRemoved && ev.cause == causeReplacedByObject {
			msg += fmt.Sprintf("; it replaced a leaf when %s was set at layer %d", ev.by, ev.layer)
		}
		return msg
	}
	ev, ok := lastEvent(m.events, path)
	if !ok {
		if n := m.noopDeletes(path); len(n) > 0 {
			return fmt.Sprintf("%s was never set (delete at layer(s) %s matched nothing)", path, joinInts(n))
		}
		return fmt.Sprintf("%s was never set", path)
	}
	prev := setLayers(m.events, path)
	switch ev.kind {
	case evDelete:
		msg := fmt.Sprintf("%s was deleted at layer %d", path, ev.layer)
		if len(prev) > 0 {
			msg += fmt.Sprintf("; previously set by layer(s) %s", joinInts(prev))
		}
		if n := noopDeletesAfter(m.events, path, ev.layer); len(n) > 0 {
			msg += fmt.Sprintf("; later delete(s) at layer(s) %s matched nothing", joinInts(n))
		}
		return msg
	case evRemoved:
		var msg string
		switch ev.cause {
		case causeDelete:
			msg = fmt.Sprintf("%s was removed when %s was deleted at layer %d", path, ev.by, ev.layer)
		case causeReplacedByLeaf:
			msg = fmt.Sprintf("%s was removed when %s was replaced by a leaf value at layer %d", path, ev.by, ev.layer)
		case causeReplacedByObject:
			msg = fmt.Sprintf("%s was a leaf, removed when %s grew an object over it at layer %d", path, ev.by, ev.layer)
		}
		if len(prev) > 0 {
			msg += fmt.Sprintf("; previously set by layer(s) %s", joinInts(prev))
		}
		return msg
	}
	return fmt.Sprintf("%s was never set", path)
}

// deleteLayers returns the layers at which an effective delete hit path.
func (m *Merger) deleteLayers(path string) []int {
	var out []int
	for _, ev := range m.events {
		if ev.kind == evDelete && ev.path == path {
			out = append(out, ev.layer)
		}
	}
	return out
}

// noopDeletes returns the layers at which a delete matched nothing at path.
func (m *Merger) noopDeletes(path string) []int {
	return noopDeletesAfter(m.events, path, -1)
}

func noopDeletesAfter(events []event, path string, layer int) []int {
	var out []int
	for _, ev := range events {
		if ev.kind == evDeleteNoop && ev.path == path && ev.layer > layer {
			out = append(out, ev.layer)
		}
	}
	return out
}

// setLayers returns all layers that set a leaf at path, oldest first.
func setLayers(events []event, path string) []int {
	var out []int
	for _, ev := range events {
		if ev.kind == evSet && ev.path == path {
			out = append(out, ev.layer)
		}
	}
	return out
}

// lastEvent returns the most recent event affecting path exactly.
func lastEvent(events []event, path string) (event, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].path == path && events[i].kind != evDeleteNoop {
			return events[i], true
		}
	}
	return event{}, false
}

// flattenLayer expands a layer into leaf path -> value. Objects recurse;
// everything else (scalars, arrays, nil, Delete) is a leaf. Empty objects
// contribute no paths and therefore do not create keys.
func flattenLayer(obj map[string]any) map[string]any {
	out := map[string]any{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if m, ok := v.(map[string]any); ok {
			for k, vv := range m {
				walk(join(prefix, k), vv)
			}
			return
		}
		out[prefix] = v
	}
	for k, v := range obj {
		walk(k, v)
	}
	return out
}

// Unflatten builds a nested map from leaf path -> value, the inverse of
// Flatten. It is a convenient way to write layers in tests.
func Unflatten(flat map[string]any) map[string]any {
	root := map[string]any{}
	for _, p := range sortedKeys(flat) {
		setNested(root, strings.Split(p, "."), flat[p])
	}
	return root
}

func setNested(root map[string]any, parts []string, v any) {
	cur := root
	for _, k := range parts[:len(parts)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func render(v any) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%v", v)
}

func joinInts(xs []int) string {
	ss := make([]string, len(xs))
	for i, x := range xs {
		ss[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(ss, ", ")
}
