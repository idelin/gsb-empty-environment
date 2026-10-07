package docindex

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func seedDocs() []Document {
	return []Document{
		{ID: "a", Fields: map[string][]string{
			"tags": {"go", "index", "go"}, // duplicate term
			"body": {},
		}},
		{ID: "b", Fields: map[string][]string{
			"tags": {"go", "sql"},
		}},
		{ID: "c", Fields: map[string][]string{
			"tags": {}, // empty array
		}},
	}
}

func TestContainsSetSemantics(t *testing.T) {
	ix := New()
	ix.BatchReplace(seedDocs())

	// "go" occurs twice in doc a but the doc appears only once.
	if got, want := ix.Contains("tags", "go"), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Contains(tags, go) = %v, want %v", got, want)
	}
	if got := ix.Contains("tags", "missing"); len(got) != 0 {
		t.Fatalf("Contains(tags, missing) = %v, want empty", got)
	}
	// Doc c has an empty tags array and must never match.
	if got := ix.Contains("tags", "go"); contains(got, "c") {
		t.Fatalf("doc with empty array matched: %v", got)
	}
}

func TestOccurrenceCountsDuplicates(t *testing.T) {
	ix := New()
	ix.BatchReplace(seedDocs())

	if got, want := ix.Occurrence("tags", "go"), 3; got != want {
		t.Fatalf("Occurrence(tags, go) = %d, want %d", got, want)
	}
	if got, want := ix.Occurrence("tags", "sql"), 1; got != want {
		t.Fatalf("Occurrence(tags, sql) = %d, want %d", got, want)
	}
	if got := ix.Occurrence("tags", "missing"); got != 0 {
		t.Fatalf("Occurrence(tags, missing) = %d, want 0", got)
	}
}

func TestDeleteWithdrawsAllOccurrences(t *testing.T) {
	ix := New()
	ix.BatchReplace(seedDocs())

	if !ix.Delete("a") {
		t.Fatal("Delete(a) = false, want true")
	}
	// Both occurrences of "go" from doc a are gone; only doc b remains.
	if got, want := ix.Occurrence("tags", "go"), 1; got != want {
		t.Fatalf("after delete: Occurrence(tags, go) = %d, want %d", got, want)
	}
	if got, want := ix.Contains("tags", "go"), []string{"b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after delete: Contains(tags, go) = %v, want %v", got, want)
	}
	if got := ix.Contains("tags", "index"); len(got) != 0 {
		t.Fatalf("after delete: Contains(tags, index) = %v, want empty", got)
	}
	if ix.Delete("a") {
		t.Fatal("second Delete(a) = true, want false")
	}
	if errs := ix.Verify(); errs != nil {
		t.Fatalf("Verify after delete: %v", errs)
	}
}

func TestBatchReplaceAtomicSameDocumentUpdate(t *testing.T) {
	ix := New()
	ix.BatchReplace(seedDocs())

	// Same-document update: doc a keeps its ID but changes terms.
	updated := []Document{
		{ID: "a", Fields: map[string][]string{"tags": {"rust", "rust"}}},
		{ID: "b", Fields: map[string][]string{"tags": {"go"}}},
	}
	ix.BatchReplace(updated)

	if got, want := ix.Occurrence("tags", "rust"), 2; got != want {
		t.Fatalf("Occurrence(tags, rust) = %d, want %d", got, want)
	}
	// Old terms of doc a must be fully withdrawn by the replacement.
	if got, want := ix.Occurrence("tags", "go"), 1; got != want {
		t.Fatalf("Occurrence(tags, go) = %d, want %d", got, want)
	}
	if got := ix.Contains("tags", "index"); len(got) != 0 {
		t.Fatalf("stale term matched after replace: %v", got)
	}
	if got, want := ix.Contains("tags", "rust"), []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Contains(tags, rust) = %v, want %v", got, want)
	}
	// Doc c was dropped by the batch.
	if ix.Len() != 2 {
		t.Fatalf("Len = %d, want 2", ix.Len())
	}
	if errs := ix.Verify(); errs != nil {
		t.Fatalf("Verify after replace: %v", errs)
	}
}

func TestBatchReplaceAtomicPublication(t *testing.T) {
	ix := New()
	ix.BatchReplace(seedDocs())

	const readers = 8
	const batches = 200
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// The index is always either the old or the new snapshot;
				// a partial batch would break this invariant. Both counts
				// are read atomically under one lock.
				counts := ix.Occurrences("tags", "go", "rust")
				goCount, rustCount := counts[0], counts[1]
				valid := (goCount == 3 && rustCount == 0) || (goCount == 0 && rustCount == 5)
				if !valid {
					t.Errorf("observed partial batch: go=%d rust=%d", goCount, rustCount)
					return
				}
			}
		}()
	}
	for i := 0; i < batches; i++ {
		if i%2 == 0 {
			ix.BatchReplace([]Document{
				{ID: "x", Fields: map[string][]string{"tags": {"rust", "rust", "rust", "rust", "rust"}}},
			})
		} else {
			ix.BatchReplace(seedDocs())
		}
	}
	close(stop)
	wg.Wait()
}

func TestVerifyFullScan(t *testing.T) {
	ix := New()
	docs := seedDocs()
	// Add a doc updated in place and delete another to exercise drift.
	ix.BatchReplace(docs)
	ix.Delete("b")
	ix.BatchReplace([]Document{
		{ID: "a", Fields: map[string][]string{"tags": {"go", "go", "go"}}},
		{ID: "d", Fields: map[string][]string{"tags": {}}},
	})
	if errs := ix.Verify(); errs != nil {
		t.Fatalf("Verify on consistent index: %v", errs)
	}

	// Corrupt the index and confirm the full scan catches it.
	ix.snap.inverted["tags"]["go"]["a"] = 99
	ix.snap.inverted["tags"]["go"]["zzz"] = 4
	ix.snap.inverted["tags"]["ghost"] = map[string]int{"z": 1}
	errs := ix.Verify()
	if len(errs) != 3 { // inflated count, missing real count, stale ghost entry
		t.Fatalf("Verify found %d errors, want 3: %v", len(errs), errs)
	}
}

func TestEmptyAndNilFields(t *testing.T) {
	ix := New()
	ix.BatchReplace([]Document{
		{ID: "nil-fields"},
		{ID: "empty-array", Fields: map[string][]string{"tags": {}}},
	})
	if ix.Len() != 2 {
		t.Fatalf("Len = %d, want 2", ix.Len())
	}
	if got := ix.Contains("tags", "go"); len(got) != 0 {
		t.Fatalf("Contains on empty arrays = %v, want empty", got)
	}
	if got := ix.Occurrence("tags", "go"); got != 0 {
		t.Fatalf("Occurrence on empty arrays = %d, want 0", got)
	}
	if errs := ix.Verify(); errs != nil {
		t.Fatalf("Verify: %v", errs)
	}
}

func contains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func ExampleIndex() {
	ix := New()
	ix.BatchReplace([]Document{
		{ID: "a", Fields: map[string][]string{"tags": {"go", "go"}}},
		{ID: "b", Fields: map[string][]string{"tags": {"go"}}},
	})
	fmt.Println(ix.Contains("tags", "go"))
	fmt.Println(ix.Occurrence("tags", "go"))
	ix.Delete("a")
	fmt.Println(ix.Occurrence("tags", "go"))
	// Output:
	// [a b]
	// 3
	// 1
}
