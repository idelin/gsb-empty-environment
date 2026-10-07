package audit

import (
	"reflect"
	"testing"
)

func mustViolations(t *testing.T, oldV, newV interface{}, r Rules) []Violation {
	t.Helper()
	vs, err := Compare(oldV, newV, r)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	return vs
}

func checkOne(t *testing.T, vs []Violation, path string, kind Kind, oldV, newV interface{}) {
	t.Helper()
	if len(vs) != 1 {
		t.Fatalf("want 1 violation, got %d: %v", len(vs), vs)
	}
	v := vs[0]
	if v.Path != path || v.Kind != kind {
		t.Fatalf("want %s %s, got %s %s", kind, path, v.Kind, v.Path)
	}
	if !reflect.DeepEqual(v.Old, oldV) || !reflect.DeepEqual(v.New, newV) {
		t.Fatalf("values: want old=%#v new=%#v, got old=%#v new=%#v", oldV, newV, v.Old, v.New)
	}
}

func TestImmutableScalarChange(t *testing.T) {
	oldV := map[string]interface{}{"spec": map[string]interface{}{"replicas": 1.0}}
	newV := map[string]interface{}{"spec": map[string]interface{}{"replicas": 2.0}}
	vs := mustViolations(t, oldV, newV, Rules{Immutable: []string{"$.spec.replicas"}})
	checkOne(t, vs, "$.spec.replicas", KindImmutable, 1.0, 2.0)
}

func TestImmutableSubtreeReplaced(t *testing.T) {
	oldV := map[string]interface{}{"spec": map[string]interface{}{"a": 1.0}}
	newV := map[string]interface{}{"spec": map[string]interface{}{"a": 2.0}}
	vs := mustViolations(t, oldV, newV, Rules{Immutable: []string{"$.spec"}})
	checkOne(t, vs, "$.spec", KindImmutable,
		map[string]interface{}{"a": 1.0}, map[string]interface{}{"a": 2.0})
}

func TestWildcardSingleLevel(t *testing.T) {
	// "$.a.*.c" protects exactly the .c child of each direct child of a.
	oldV := map[string]interface{}{"a": map[string]interface{}{"x": map[string]interface{}{"c": 1.0}}}
	newV := map[string]interface{}{"a": map[string]interface{}{"x": map[string]interface{}{"c": 2.0}}}
	vs := mustViolations(t, oldV, newV, Rules{Immutable: []string{"$.a.*.c"}})
	checkOne(t, vs, "$.a.x.c", KindImmutable, 1.0, 2.0)

	// A change at "$.a.x" itself is not covered: "*" spans one level only.
	oldV2 := map[string]interface{}{"a": map[string]interface{}{"x": 1.0}}
	newV2 := map[string]interface{}{"a": map[string]interface{}{"x": 2.0}}
	if vs := mustViolations(t, oldV2, newV2, Rules{Immutable: []string{"$.a.*.c"}}); vs != nil {
		t.Fatalf("want no violations, got %v", vs)
	}

	// Deeper change is reported at the shallowest protected path "$.a.x.c".
	oldV3 := map[string]interface{}{"a": map[string]interface{}{"x": map[string]interface{}{"c": map[string]interface{}{"d": 1.0}}}}
	newV3 := map[string]interface{}{"a": map[string]interface{}{"x": map[string]interface{}{"c": map[string]interface{}{"d": 2.0}}}}
	vs3 := mustViolations(t, oldV3, newV3, Rules{Immutable: []string{"$.a.*.c"}})
	checkOne(t, vs3, "$.a.x.c", KindImmutable,
		map[string]interface{}{"d": 1.0}, map[string]interface{}{"d": 2.0})
}

func TestAddRules(t *testing.T) {
	oldV := map[string]interface{}{"a": 1.0}
	newV := map[string]interface{}{"a": 1.0, "b": 2.0}

	vs := mustViolations(t, oldV, newV, Rules{})
	checkOne(t, vs, "$.b", KindAdd, Absent, 2.0)

	if vs := mustViolations(t, oldV, newV, Rules{AllowAdd: []string{"$.b"}}); vs != nil {
		t.Fatalf("want allowed, got %v", vs)
	}
	if vs := mustViolations(t, oldV, newV, Rules{AllowAdd: []string{"$.*"}}); vs != nil {
		t.Fatalf("want allowed by wildcard, got %v", vs)
	}
	// A pattern for a deeper path does not permit adding its parent.
	vs2 := mustViolations(t, oldV, newV, Rules{AllowAdd: []string{"$.b.c"}})
	checkOne(t, vs2, "$.b", KindAdd, Absent, 2.0)
}

func TestAddNestedWholesale(t *testing.T) {
	oldV := map[string]interface{}{}
	newV := map[string]interface{}{"b": map[string]interface{}{"c": map[string]interface{}{"d": 1.0}}}
	if vs := mustViolations(t, oldV, newV, Rules{AllowAdd: []string{"$.b"}}); vs != nil {
		t.Fatalf("want wholesale add allowed, got %v", vs)
	}
}

func TestRemoveRules(t *testing.T) {
	oldV := map[string]interface{}{"a": 1.0, "b": 2.0}
	newV := map[string]interface{}{"a": 1.0}

	vs := mustViolations(t, oldV, newV, Rules{})
	checkOne(t, vs, "$.b", KindRemove, 2.0, Absent)

	if vs := mustViolations(t, oldV, newV, Rules{AllowRemove: []string{"$.b"}}); vs != nil {
		t.Fatalf("want allowed, got %v", vs)
	}
}

func TestParentRemovalChecksProtectedDescendant(t *testing.T) {
	// Removal of $.a.b is allowed, but the immutable descendant $.a.b.c
	// must still be verified leaf by leaf.
	oldV := map[string]interface{}{"a": map[string]interface{}{"b": map[string]interface{}{"c": 1.0, "d": 2.0}}}
	newV := map[string]interface{}{"a": map[string]interface{}{}}
	vs := mustViolations(t, oldV, newV, Rules{
		Immutable:   []string{"$.a.b.c"},
		AllowRemove: []string{"$.a.b"},
	})
	checkOne(t, vs, "$.a.b.c", KindImmutable, 1.0, Absent)
}

func TestParentReplacementChecksProtectedDescendant(t *testing.T) {
	// $.a is replaced wholesale by a scalar; the protected descendant
	// $.a.b is still reported.
	oldV := map[string]interface{}{"a": map[string]interface{}{"b": 1.0, "c": 2.0}}
	newV := map[string]interface{}{"a": "gone"}
	vs := mustViolations(t, oldV, newV, Rules{Immutable: []string{"$.a.b"}})
	checkOne(t, vs, "$.a.b", KindImmutable, 1.0, Absent)
}

func TestParentRemovalCollapsesToShallowest(t *testing.T) {
	// Without AllowRemove, the removal of $.a is itself a violation and is
	// shallower than the immutable violation at $.a.b.
	oldV := map[string]interface{}{"a": map[string]interface{}{"b": 1.0}}
	newV := map[string]interface{}{}
	vs := mustViolations(t, oldV, newV, Rules{Immutable: []string{"$.a.b"}})
	checkOne(t, vs, "$.a", KindRemove, map[string]interface{}{"b": 1.0}, Absent)
}

func TestArrayElementAdd(t *testing.T) {
	oldV := map[string]interface{}{"l": []interface{}{1.0, 2.0}}
	newV := map[string]interface{}{"l": []interface{}{1.0, 2.0, 3.0}}

	vs := mustViolations(t, oldV, newV, Rules{})
	checkOne(t, vs, "$.l[2]", KindAdd, Absent, 3.0)

	if vs := mustViolations(t, oldV, newV, Rules{AllowAdd: []string{"$.l[*]"}}); vs != nil {
		t.Fatalf("want allowed by [*], got %v", vs)
	}
}

func TestArrayElementRemove(t *testing.T) {
	oldV := map[string]interface{}{"l": []interface{}{1.0, 2.0}}
	newV := map[string]interface{}{"l": []interface{}{1.0}}
	vs := mustViolations(t, oldV, newV, Rules{})
	checkOne(t, vs, "$.l[1]", KindRemove, 2.0, Absent)
}

func TestArrayElementModifyAllowed(t *testing.T) {
	oldV := map[string]interface{}{"l": []interface{}{1.0}}
	newV := map[string]interface{}{"l": []interface{}{2.0}}
	if vs := mustViolations(t, oldV, newV, Rules{}); vs != nil {
		t.Fatalf("want modify allowed, got %v", vs)
	}
}

func TestRootReplace(t *testing.T) {
	oldV := map[string]interface{}{"a": 1.0}
	if vs := mustViolations(t, oldV, "x", Rules{}); vs != nil {
		t.Fatalf("want root modify allowed, got %v", vs)
	}
	vs := mustViolations(t, oldV, "x", Rules{Immutable: []string{"$"}})
	checkOne(t, vs, "$", KindImmutable, oldV, "x")
}

func TestModifyAllowedByDefault(t *testing.T) {
	oldV := map[string]interface{}{"a": 1.0}
	newV := map[string]interface{}{"a": 2.0}
	if vs := mustViolations(t, oldV, newV, Rules{}); vs != nil {
		t.Fatalf("want modify allowed, got %v", vs)
	}
}

func TestEmptyContainers(t *testing.T) {
	oldV := map[string]interface{}{"a": map[string]interface{}{}}
	newV := map[string]interface{}{"a": map[string]interface{}{"b": 1.0}}
	vs := mustViolations(t, oldV, newV, Rules{})
	checkOne(t, vs, "$.a.b", KindAdd, Absent, 1.0)

	vs2 := mustViolations(t, newV, oldV, Rules{})
	checkOne(t, vs2, "$.a.b", KindRemove, 1.0, Absent)
}

func TestAdditionUnderImmutable(t *testing.T) {
	oldV := map[string]interface{}{"a": map[string]interface{}{"x": 1.0}}
	newV := map[string]interface{}{"a": map[string]interface{}{"x": 1.0, "y": 2.0}}
	vs := mustViolations(t, oldV, newV, Rules{Immutable: []string{"$.a"}})
	checkOne(t, vs, "$.a", KindImmutable,
		map[string]interface{}{"x": 1.0}, map[string]interface{}{"x": 1.0, "y": 2.0})
}

func TestProtectedPathCreatedInsideAllowedAdd(t *testing.T) {
	// Adding $.a is allowed, but creating the immutable path $.a.b inside
	// it is still a violation.
	oldV := map[string]interface{}{}
	newV := map[string]interface{}{"a": map[string]interface{}{"b": 1.0}}
	vs := mustViolations(t, oldV, newV, Rules{
		Immutable: []string{"$.a.b"},
		AllowAdd:  []string{"$.a"},
	})
	checkOne(t, vs, "$.a.b", KindImmutable, Absent, 1.0)

	// Without AllowAdd the shallower add violation at $.a wins.
	vs2 := mustViolations(t, oldV, newV, Rules{Immutable: []string{"$.a.b"}})
	checkOne(t, vs2, "$.a", KindAdd, Absent, map[string]interface{}{"b": 1.0})
}

func TestCompareJSON(t *testing.T) {
	oldJSON := []byte(`{"metadata":{"name":"app"},"spec":{"replicas":1}}`)
	newJSON := []byte(`{"metadata":{"name":"app","labels":{"x":"y"}},"spec":{"replicas":2}}`)
	vs, err := CompareJSON(oldJSON, newJSON, Rules{
		Immutable: []string{"$.metadata.name"},
		AllowAdd:  []string{"$.metadata.labels"},
	})
	if err != nil {
		t.Fatalf("CompareJSON: %v", err)
	}
	if vs != nil {
		t.Fatalf("want compliant, got %v", vs)
	}

	vs, err = CompareJSON(oldJSON, []byte(`{"metadata":{"name":"other"},"spec":{"replicas":1}}`), Rules{
		Immutable: []string{"$.metadata.name"},
	})
	if err != nil {
		t.Fatalf("CompareJSON: %v", err)
	}
	checkOne(t, vs, "$.metadata.name", KindImmutable, "app", "other")
}

func TestBadPatterns(t *testing.T) {
	for _, pat := range []string{"a..b", "a[x]", "a[", "a.", "a[0]b"} {
		if _, err := Compare(nil, nil, Rules{Immutable: []string{pat}}); err == nil {
			t.Fatalf("want error for pattern %q", pat)
		}
	}
}

func TestMultipleViolationsSorted(t *testing.T) {
	oldV := map[string]interface{}{"b": 1.0, "a": 1.0}
	newV := map[string]interface{}{}
	vs := mustViolations(t, oldV, newV, Rules{})
	if len(vs) != 2 {
		t.Fatalf("want 2 violations, got %v", vs)
	}
	if vs[0].Path != "$.a" || vs[1].Path != "$.b" {
		t.Fatalf("want sorted paths, got %v", vs)
	}
}
