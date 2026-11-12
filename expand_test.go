package taskpkg

import (
	"errors"
	"testing"
)

func keysOf(rows []Row) []string {
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.Key
	}
	return keys
}

func assertKeys(t *testing.T, rows []Row, want []string) {
	t.Helper()
	got := keysOf(rows)
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key %d = %q, want %q\ngot:  %v\nwant: %v", i, got[i], want[i], got, want)
		}
	}
}

// Multi-level expansion mixing zip and cross, exercising null (unknown),
// empty arrays (zero rows) and zip padding (missing), all in one document.
func TestExpandMixedLevels(t *testing.T) {
	doc := map[string]any{
		"items": []any{
			map[string]any{
				"id": "i1",
				"sizes": []any{
					map[string]any{"id": "s1", "tags": []any{
						map[string]any{"id": "t1"},
						map[string]any{"id": "t2"},
					}},
					map[string]any{"id": "s2", "tags": []any{}},
				},
				"colors": []any{map[string]any{"id": "c1"}},
			},
			map[string]any{"id": "i2", "sizes": []any{}, "colors": nil},
			map[string]any{"id": "i3", "sizes": []any{}, "colors": []any{}},
		},
	}
	spec := Spec{
		IDField: "id",
		Levels: []Level{
			{Name: "item", Fields: []string{"items"}, Rule: Cross},
			{Name: "variant", Fields: []string{"sizes", "colors"}, Rule: Zip},
			{Name: "tag", Fields: []string{"tags"}, Rule: Cross},
		},
	}
	rows, err := Expand(doc, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	assertKeys(t, rows, []string{
		"items#i1/sizes#s1/colors#c1/tags#t1/",
		"items#i1/sizes#s1/colors#c1/tags#t2/",
		"items#i1/sizes#s1/colors#c1/tags?unknown/",
		"items#i1/sizes#s2/colors!missing@1/tags?unknown/",
		"items#i2/sizes!missing@0/colors?unknown/tags?unknown/",
	})
	if rows[0].Path != "items[0](#i1)/sizes[0](#s1)/colors[0](#c1)/tags[0](#t1)" {
		t.Fatalf("unexpected path: %s", rows[0].Path)
	}
	if rows[3].Path != "items[0](#i1)/sizes[1](#s2)/colors(!missing)/tags(?unknown)" {
		t.Fatalf("unexpected path: %s", rows[3].Path)
	}
}

// Zip pairs by position and pads the shorter side with missing markers.
func TestZipPadding(t *testing.T) {
	doc := map[string]any{
		"items":  []any{map[string]any{"id": "i1"}, map[string]any{"id": "i2"}, map[string]any{"id": "i3"}},
		"promos": []any{map[string]any{"id": "p1"}},
	}
	spec := Spec{
		IDField: "id",
		Levels:  []Level{{Name: "line", Fields: []string{"items", "promos"}, Rule: Zip}},
	}
	rows, err := Expand(doc, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	assertKeys(t, rows, []string{
		"items#i1/promos#p1/",
		"items#i2/promos!missing@1/",
		"items#i3/promos!missing@2/",
	})
	if rows[1].Refs[1].Kind != RefMissing {
		t.Fatalf("expected missing marker, got %v", rows[1].Refs[1].Kind)
	}
}

// A null field participates as one unknown element; an empty array yields
// the empty set and therefore zero rows under cross.
func TestNullUnknownAndEmptySet(t *testing.T) {
	spec := Spec{
		IDField: "id",
		Levels:  []Level{{Name: "g", Fields: []string{"a", "b"}, Rule: Cross}},
	}
	rows, err := Expand(map[string]any{
		"a": nil,
		"b": []any{map[string]any{"id": "b1"}, map[string]any{"id": "b2"}},
	}, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	assertKeys(t, rows, []string{"a?unknown/b#b1/", "a?unknown/b#b2/"})
	if rows[0].Refs[0].Kind != RefUnknown {
		t.Fatalf("expected unknown element, got %v", rows[0].Refs[0].Kind)
	}

	rows, err = Expand(map[string]any{
		"a": []any{},
		"b": []any{map[string]any{"id": "b1"}},
	}, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty array must yield zero rows, got %v", keysOf(rows))
	}

	// Zip with one empty and one null side still emits the padded tuple.
	rows, err = Expand(map[string]any{"a": []any{}, "b": nil}, Spec{
		IDField: "id",
		Levels:  []Level{{Name: "g", Fields: []string{"a", "b"}, Rule: Zip}},
	})
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	assertKeys(t, rows, []string{"a!missing@0/b?unknown/"})
}

// Duplicate values with different stable IDs produce distinct rows, and a
// re-expansion of the same document is a no-op.
func TestDuplicateValuesDistinctIDs(t *testing.T) {
	doc := map[string]any{
		"items": []any{
			map[string]any{"id": "i1", "sku": "A"},
			map[string]any{"id": "i2", "sku": "A"},
		},
	}
	spec := Spec{
		IDField: "id",
		Levels:  []Level{{Name: "item", Fields: []string{"items"}, Rule: Cross}},
	}
	rows, err := Expand(doc, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	assertKeys(t, rows, []string{"items#i1/", "items#i2/"})

	again, err := Expand(doc, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if b := Plan(rows, again); !b.Empty() {
		t.Fatalf("re-expansion must be a no-op, got %+v", b)
	}
}

// Exceeding the row budget fails the whole expansion.
func TestBudgetExceeded(t *testing.T) {
	doc := map[string]any{
		"items": []any{
			map[string]any{"id": "i1"},
			map[string]any{"id": "i2"},
			map[string]any{"id": "i3"},
		},
	}
	spec := Spec{
		IDField: "id",
		MaxRows: 2,
		Levels:  []Level{{Name: "item", Fields: []string{"items"}, Rule: Cross}},
	}
	if _, err := Expand(doc, spec); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
	spec.MaxRows = 3
	if rows, err := Expand(doc, spec); err != nil || len(rows) != 3 {
		t.Fatalf("budget equal to row count must pass: rows=%d err=%v", len(rows), err)
	}
}

func TestSpecValidation(t *testing.T) {
	if _, err := Expand(map[string]any{}, Spec{}); err == nil {
		t.Fatal("expected error for spec without levels")
	}
	if _, err := Expand(map[string]any{}, Spec{Levels: []Level{{Name: "x"}}}); err == nil {
		t.Fatal("expected error for level without fields")
	}
}
