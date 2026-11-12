package taskpkg

import (
	"errors"
	"sort"
	"testing"
)

func sortedKeys(rows []Row) []string {
	keys := keysOf(rows)
	sort.Strings(keys)
	return keys
}

// applyBatch applies a delta to a published row set and returns the new set.
func applyBatch(prev []Row, b Batch) []Row {
	retract := make(map[string]int)
	for _, r := range b.Retractions {
		retract[r.Key+"|"+r.Hash]++
	}
	var out []Row
	for _, r := range prev {
		k := r.Key + "|" + r.Hash
		if retract[k] > 0 {
			retract[k]--
			continue
		}
		out = append(out, r)
	}
	return append(out, b.Additions...)
}

func zipSpec() Spec {
	return Spec{
		IDField: "id",
		Levels:  []Level{{Name: "line", Fields: []string{"items", "promos"}, Rule: Zip}},
	}
}

func docLines(itemIDs, promoIDs []string) map[string]any {
	mk := func(ids []string) []any {
		arr := make([]any, len(ids))
		for i, id := range ids {
			arr[i] = map[string]any{"id": id}
		}
		return arr
	}
	return map[string]any{"items": mk(itemIDs), "promos": mk(promoIDs)}
}

// Inserting an element by ID shifts zip pairings: only the affected rows
// are retracted and added.
func TestInsertShiftsZipPairing(t *testing.T) {
	spec := zipSpec()
	prev, err := Expand(docLines([]string{"i1", "i2"}, []string{"p1", "p2"}), spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	batch, next, err := Publish(prev, docLines([]string{"i0", "i1", "i2"}, []string{"p1", "p2"}), spec)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	assertKeys(t, batch.Retractions, []string{"items#i1/promos#p1/", "items#i2/promos#p2/"})
	assertKeys(t, batch.Additions, []string{"items#i0/promos#p1/", "items#i1/promos#p2/", "items#i2/promos!missing@2/"})

	fresh, err := Expand(docLines([]string{"i0", "i1", "i2"}, []string{"p1", "p2"}), spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if got, want := sortedKeys(applyBatch(prev, batch)), sortedKeys(fresh); !equalKeys(got, want) {
		t.Fatalf("applied batch diverges from full re-expansion\ngot:  %v\nwant: %v", got, want)
	}
	_ = next
}

// Deleting an element retracts exactly its rows; the now unpaired promo on
// the longer side surfaces as a new row carrying a missing marker.
func TestDeleteByElementID(t *testing.T) {
	spec := zipSpec()
	prev, err := Expand(docLines([]string{"i1", "i2"}, []string{"p1", "p2"}), spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	batch, _, err := Publish(prev, docLines([]string{"i1"}, []string{"p1", "p2"}), spec)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	assertKeys(t, batch.Retractions, []string{"items#i2/promos#p2/"})
	assertKeys(t, batch.Additions, []string{"items!missing@1/promos#p2/"})
}

// Moving an element inside a cross level keeps every row key stable, so
// the batch is empty; moving inside a zip level only rewrites the rows
// whose pairing actually changed.
func TestMoveByElementID(t *testing.T) {
	crossSpec := Spec{
		IDField: "id",
		Levels:  []Level{{Name: "item", Fields: []string{"items"}, Rule: Cross}},
	}
	v1 := map[string]any{"items": []any{map[string]any{"id": "i1"}, map[string]any{"id": "i2"}}}
	v2 := map[string]any{"items": []any{map[string]any{"id": "i2"}, map[string]any{"id": "i1"}}}
	prev, err := Expand(v1, crossSpec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	batch, _, err := Publish(prev, v2, crossSpec)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !batch.Empty() {
		t.Fatalf("reorder under cross must be a no-op, got %+v", batch)
	}

	spec := zipSpec()
	prev, err = Expand(docLines([]string{"i1", "i2"}, []string{"p1", "p2"}), spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	batch, _, err = Publish(prev, docLines([]string{"i2", "i1"}, []string{"p1", "p2"}), spec)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	assertKeys(t, batch.Retractions, []string{"items#i1/promos#p1/", "items#i2/promos#p2/"})
	assertKeys(t, batch.Additions, []string{"items#i2/promos#p1/", "items#i1/promos#p2/"})
}

// A correction keeps the element ID but changes a value: the row key is
// stable while its hash changes, surfacing as retract plus add.
func TestCorrectionEvent(t *testing.T) {
	spec := Spec{
		IDField: "id",
		Levels:  []Level{{Name: "item", Fields: []string{"items"}, Rule: Cross}},
	}
	v1 := map[string]any{"items": []any{map[string]any{"id": "i1", "qty": 1}}}
	v2 := map[string]any{"items": []any{map[string]any{"id": "i1", "qty": 2}}}
	prev, err := Expand(v1, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	batch, next, err := Publish(prev, v2, spec)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	assertKeys(t, batch.Retractions, []string{"items#i1/"})
	assertKeys(t, batch.Additions, []string{"items#i1/"})
	if batch.Retractions[0].Hash == batch.Additions[0].Hash {
		t.Fatal("correction must change the row hash")
	}
	if next[0].Hash != batch.Additions[0].Hash {
		t.Fatal("published rows must carry the new hash")
	}
}

// Full re-expansion cross-check: a sequence of insert, delete, move and
// correction events is published step by step; after every step the
// applied batches must reproduce a from-scratch expansion exactly.
func TestFullReexpansionConsistency(t *testing.T) {
	spec := Spec{
		IDField: "id",
		Levels: []Level{
			{Name: "item", Fields: []string{"items"}, Rule: Cross},
			{Name: "variant", Fields: []string{"sizes", "colors"}, Rule: Zip},
		},
	}
	item := func(id string, sizeIDs, colorIDs []string) map[string]any {
		mk := func(ids []string) []any {
			arr := make([]any, len(ids))
			for i, id := range ids {
				arr[i] = map[string]any{"id": id}
			}
			return arr
		}
		return map[string]any{"id": id, "sizes": mk(sizeIDs), "colors": mk(colorIDs)}
	}
	docs := []map[string]any{
		{"items": []any{item("i1", []string{"s1", "s2"}, []string{"c1"})}},
		{"items": []any{ // insert item i0 and color c2
			item("i0", []string{"s9"}, nil),
			item("i1", []string{"s1", "s2"}, []string{"c1", "c2"}),
		}},
		{"items": []any{ // delete s1, move c2 before c1
			item("i0", []string{"s9"}, nil),
			item("i1", []string{"s2"}, []string{"c2", "c1"}),
		}},
		{"items": []any{ // correction: same IDs, changed value
			item("i0", []string{"s9"}, nil),
			func() map[string]any {
				m := item("i1", []string{"s2"}, []string{"c2", "c1"})
				m["sizes"].([]any)[0].(map[string]any)["v"] = "XL"
				return m
			}(),
		}},
	}
	var published []Row
	for step, doc := range docs {
		batch, next, err := Publish(published, doc, spec)
		if err != nil {
			t.Fatalf("step %d: Publish: %v", step, err)
		}
		published = applyBatch(published, batch)
		if got, want := sortedKeys(published), sortedKeys(next); !equalKeys(got, want) {
			t.Fatalf("step %d: applied batches diverge from full re-expansion\ngot:  %v\nwant: %v", step, got, want)
		}
	}
}

// When the row budget is exceeded the whole publication is withheld: the
// batch is empty and the previously published state stays authoritative.
func TestBudgetExceededPublishesNothing(t *testing.T) {
	spec := Spec{
		IDField: "id",
		MaxRows: 2,
		Levels:  []Level{{Name: "item", Fields: []string{"items"}, Rule: Cross}},
	}
	small := map[string]any{"items": []any{map[string]any{"id": "i1"}, map[string]any{"id": "i2"}}}
	big := map[string]any{"items": []any{map[string]any{"id": "i1"}, map[string]any{"id": "i2"}, map[string]any{"id": "i3"}}}

	published, err := Expand(small, spec)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	batch, next, err := Publish(published, big, spec)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
	if !batch.Empty() || next != nil {
		t.Fatalf("budget breach must publish nothing, got batch=%+v next=%v", batch, next)
	}
	if got := sortedKeys(published); !equalKeys(got, []string{"items#i1/", "items#i2/"}) {
		t.Fatalf("published state must stay unchanged, got %v", got)
	}
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
