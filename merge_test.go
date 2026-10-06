package cfgmerge

import (
	"reflect"
	"strings"
	"testing"
)

// merge applies flat layers (leaf path -> value) in order and returns the
// Merger. Layers are written in the same path-expanded model that the
// assertions use.
func merge(flatLayers ...map[string]any) *Merger {
	m := NewMerger()
	for _, fl := range flatLayers {
		m.Apply(Unflatten(fl))
	}
	return m
}

func assertFlat(t *testing.T, m *Merger, want map[string]any) {
	t.Helper()
	if got := m.Flatten(); !reflect.DeepEqual(got, want) {
		t.Errorf("Flatten() = %v, want %v", got, want)
	}
}

func assertSource(t *testing.T, m *Merger, path string, wantLayer int) {
	t.Helper()
	got, ok := m.Source(path)
	if !ok {
		t.Fatalf("Source(%q): path missing, want layer %d", path, wantLayer)
	}
	if got != wantLayer {
		t.Errorf("Source(%q) = %d, want %d", path, got, wantLayer)
	}
}

func assertOverridden(t *testing.T, m *Merger, path string, want []int) {
	t.Helper()
	if got := m.Overridden(path); !reflect.DeepEqual(got, want) {
		t.Errorf("Overridden(%q) = %v, want %v", path, got, want)
	}
}

func assertExplain(t *testing.T, m *Merger, path string, wantParts ...string) {
	t.Helper()
	got := m.Explain(path)
	for _, p := range wantParts {
		if !strings.Contains(got, p) {
			t.Errorf("Explain(%q) = %q, want it to contain %q", path, got, p)
		}
	}
}

func TestObjectsMergeRecursively(t *testing.T) {
	m := merge(
		map[string]any{"server.host": "a", "server.port": 80, "debug": false},
		map[string]any{"server.port": 8080, "server.tls.enabled": true},
	)
	assertFlat(t, m, map[string]any{
		"server.host":        "a",
		"server.port":        8080,
		"server.tls.enabled": true,
		"debug":              false,
	})
	assertSource(t, m, "server.host", 0)
	assertSource(t, m, "server.port", 1)
	assertOverridden(t, m, "server.port", []int{0})
	assertOverridden(t, m, "server.host", nil)
}

func TestArraysReplaceWholesale(t *testing.T) {
	m := merge(
		map[string]any{"tags": []any{"a", "b", "c"}, "nested.list": []any{1}},
		map[string]any{"tags": []any{"z"}},
	)
	assertFlat(t, m, map[string]any{
		"tags":        []any{"z"},
		"nested.list": []any{1},
	})
	assertSource(t, m, "tags", 1)
	assertOverridden(t, m, "tags", []int{0})
}

func TestNullKeepsKeyDeleteRemoves(t *testing.T) {
	m := merge(
		map[string]any{"keep": 1, "drop": 2},
		map[string]any{"keep": nil, "drop": Delete},
	)
	v, ok := m.Lookup("keep")
	if !ok || v != nil {
		t.Errorf("Lookup(keep) = %v, %v; want nil, true", v, ok)
	}
	if _, ok := m.Lookup("drop"); ok {
		t.Error("Lookup(drop): key should be removed")
	}
	assertFlat(t, m, map[string]any{"keep": nil})
	assertSource(t, m, "keep", 1)
	assertExplain(t, m, "drop", "deleted at layer 1", "previously set by layer(s) 0")
}

func TestTypeSwitchObjectToLeaf(t *testing.T) {
	m := merge(
		map[string]any{"a.b": 1, "a.c": 2},
		map[string]any{"a": "scalar"},
	)
	assertFlat(t, m, map[string]any{"a": "scalar"})
	assertSource(t, m, "a", 1)
	assertExplain(t, m, "a.b", "removed when a was replaced by a leaf value at layer 1", "previously set by layer(s) 0")
	assertExplain(t, m, "a.c", "replaced by a leaf value at layer 1")
}

func TestTypeSwitchLeafToObject(t *testing.T) {
	m := merge(
		map[string]any{"a": "scalar"},
		map[string]any{"a.b": 1},
	)
	assertFlat(t, m, map[string]any{"a.b": 1})
	assertExplain(t, m, "a", "is an object", "replaced a leaf when a.b was set at layer 1")
}

func TestTypeSwitchArrayAndObject(t *testing.T) {
	m := merge(
		map[string]any{"x.y": 1},         // object
		map[string]any{"x": []any{1, 2}}, // object -> array
		map[string]any{"x.z": 3},         // array -> object
	)
	assertFlat(t, m, map[string]any{"x.z": 3})
	assertExplain(t, m, "x.y", "removed when x was replaced by a leaf value at layer 1")
	assertExplain(t, m, "x", "is an object", "replaced a leaf when x.z was set at layer 2")
}

func TestRepeatedDelete(t *testing.T) {
	m := merge(
		map[string]any{"a": 1},
		map[string]any{"a": Delete}, // effective
		map[string]any{"a": Delete}, // no-op
		map[string]any{"a": Delete}, // no-op
	)
	assertFlat(t, m, map[string]any{})
	assertExplain(t, m, "a",
		"deleted at layer 1",
		"previously set by layer(s) 0",
		"later delete(s) at layer(s) 2, 3 matched nothing",
	)
}

func TestDeleteNeverSet(t *testing.T) {
	m := merge(
		map[string]any{"a": 1},
		map[string]any{"ghost": Delete},
	)
	assertFlat(t, m, map[string]any{"a": 1})
	assertExplain(t, m, "ghost", "never set", "delete at layer(s) 1 matched nothing")
	assertExplain(t, m, "missing", "never set")
}

func TestDeleteRemovesSubtree(t *testing.T) {
	m := merge(
		map[string]any{"a.b.c": 1, "a.b.d": 2, "a.e": 3},
		map[string]any{"a.b": Delete},
	)
	assertFlat(t, m, map[string]any{"a.e": 3})
	assertExplain(t, m, "a.b.c", "removed when a.b was deleted at layer 1", "previously set by layer(s) 0")
}

func TestParentReplacedThenRegrown(t *testing.T) {
	m := merge(
		map[string]any{"a.b.c": 1, "a.b.d": 2},
		map[string]any{"a": "scalar"}, // parent object replaced by leaf
		map[string]any{"a.b.e": 3},    // object regrows over the leaf
	)
	assertFlat(t, m, map[string]any{"a.b.e": 3})
	assertSource(t, m, "a.b.e", 2)
	// a.b.c is still explained by the layer-1 replacement, not the regrowth.
	assertExplain(t, m, "a.b.c", "removed when a was replaced by a leaf value at layer 1")
	assertExplain(t, m, "a", "is an object", "replaced a leaf when a.b.e was set at layer 2")
}

func TestDeleteThenReset(t *testing.T) {
	m := merge(
		map[string]any{"a": 1},
		map[string]any{"a": Delete},
		map[string]any{"a": 5},
	)
	assertFlat(t, m, map[string]any{"a": 5})
	assertSource(t, m, "a", 2)
	assertOverridden(t, m, "a", []int{0})
	assertExplain(t, m, "a", "set by layer 2", "overrode layer(s) 0", "deleted at layer(s) 1 before being re-set")
}

func TestOverriddenChain(t *testing.T) {
	m := merge(
		map[string]any{"k": "v0"},
		map[string]any{"k": "v1"},
		map[string]any{"k": "v2"},
		map[string]any{"k": "v3"},
	)
	assertSource(t, m, "k", 3)
	assertOverridden(t, m, "k", []int{0, 1, 2})
	assertExplain(t, m, "k", "set by layer 3", "overrode layer(s) 0, 1, 2")
}

func TestMapRoundTrip(t *testing.T) {
	m := merge(
		map[string]any{"a.b": 1, "a.c": []any{"x"}, "n": nil},
		map[string]any{"a.b": 2},
	)
	want := map[string]any{
		"a": map[string]any{"b": 2, "c": []any{"x"}},
		"n": nil,
	}
	if got := m.Map(); !reflect.DeepEqual(got, want) {
		t.Errorf("Map() = %v, want %v", got, want)
	}
}

func TestEmptyObjectContributesNothing(t *testing.T) {
	m := merge(
		map[string]any{"a": map[string]any{}},
	)
	assertFlat(t, m, map[string]any{})
	assertExplain(t, m, "a", "never set")
}
