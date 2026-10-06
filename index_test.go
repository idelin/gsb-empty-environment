package docindex

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// expected is recomputed from a full scan over the given documents,
// independently of the index internals.
type expected struct {
	contains    map[string]map[string][]string // field -> term -> sorted doc IDs
	occurrences map[string]map[string]int      // field -> term -> total count
}

// fullScan derives expected query and aggregation results by scanning every
// document. Later documents with the same ID replace earlier ones.
func fullScan(docs []Document) expected {
	live := make(map[string]Document)
	order := make(map[string]int)
	for _, doc := range docs {
		live[doc.ID] = doc
		order[doc.ID]++
	}
	exp := expected{
		contains:    make(map[string]map[string][]string),
		occurrences: make(map[string]map[string]int),
	}
	for id, doc := range live {
		for field, tokens := range doc.Fields {
			seen := make(map[string]bool)
			for _, tok := range tokens {
				if exp.occurrences[field] == nil {
					exp.occurrences[field] = make(map[string]int)
				}
				exp.occurrences[field][tok]++
				if !seen[tok] {
					seen[tok] = true
					if exp.contains[field] == nil {
						exp.contains[field] = make(map[string][]string)
					}
					exp.contains[field][tok] = append(exp.contains[field][tok], id)
				}
			}
		}
	}
	for field := range exp.contains {
		for term := range exp.contains[field] {
			sort.Strings(exp.contains[field][term])
		}
	}
	return exp
}

// verifyAgainstScan checks the index against a full document scan: set
// semantics for Contains, repetition counting for Occurrences, including
// terms that appear only as empty arrays or not at all.
func verifyAgainstScan(t *testing.T, ix *Index, docs []Document, probeTerms []string) {
	t.Helper()
	exp := fullScan(docs)

	fields := map[string]bool{}
	terms := map[string]bool{}
	for _, doc := range docs {
		for field, tokens := range doc.Fields {
			fields[field] = true
			for _, tok := range tokens {
				terms[tok] = true
			}
		}
	}
	for _, tok := range probeTerms {
		terms[tok] = true
	}
	fields["no-such-field"] = true

	for field := range fields {
		for term := range terms {
			wantIDs := exp.contains[field][term]
			if wantIDs == nil {
				wantIDs = []string{}
			}
			if got := ix.Contains(field, term); !reflect.DeepEqual(got, wantIDs) {
				t.Errorf("Contains(%q, %q) = %v, want %v", field, term, got, wantIDs)
			}
			if got, want := ix.Occurrences(field, term), exp.occurrences[field][term]; got != want {
				t.Errorf("Occurrences(%q, %q) = %d, want %d", field, term, got, want)
			}
		}
	}
}

func fixtureDocs() []Document {
	return []Document{
		{ID: "d1", Fields: map[string][]string{
			"tags": {"go", "index", "go", "go"}, // repeated tokens
			"lang": {"go"},
		}},
		{ID: "d2", Fields: map[string][]string{
			"tags": {"go", "test"},
			"lang": {"go", "go"},
		}},
		{ID: "d3", Fields: map[string][]string{
			"tags": {}, // empty array
			"lang": {"rust"},
		}},
		{ID: "d4", Fields: map[string][]string{}}, // no fields at all
		{ID: "d5", Fields: map[string][]string{
			"tags": {"test", "test"},
		}},
	}
}

func TestContainsSetSemanticsAndOccurrenceCounts(t *testing.T) {
	docs := fixtureDocs()
	ix := New()
	for _, doc := range docs {
		ix.Upsert(doc)
	}
	verifyAgainstScan(t, ix, docs, []string{"missing-term"})

	// Spot-check the key semantics explicitly.
	if got := ix.Contains("tags", "go"); !reflect.DeepEqual(got, []string{"d1", "d2"}) {
		t.Errorf("Contains(tags, go) = %v, want [d1 d2] (set semantics)", got)
	}
	if got := ix.Occurrences("tags", "go"); got != 4 {
		t.Errorf("Occurrences(tags, go) = %d, want 4 (3 in d1 + 1 in d2)", got)
	}
	if got := ix.Occurrences("lang", "go"); got != 3 {
		t.Errorf("Occurrences(lang, go) = %d, want 3", got)
	}
	if got := ix.Contains("tags", "test"); !reflect.DeepEqual(got, []string{"d2", "d5"}) {
		t.Errorf("Contains(tags, test) = %v, want [d2 d5]", got)
	}
}

func TestDeleteWithdrawsAllOccurrences(t *testing.T) {
	docs := fixtureDocs()
	ix := New()
	for _, doc := range docs {
		ix.Upsert(doc)
	}

	ix.Delete("d1") // contributes 3x "go" in tags, 1x "go" in lang
	ix.Delete("d5") // contributes 2x "test" in tags
	ix.Delete("d1") // deleting twice is a no-op
	ix.Delete("never-existed")

	remaining := []Document{docs[1], docs[2], docs[3]}
	verifyAgainstScan(t, ix, remaining, []string{"go", "test"})

	if got := ix.Occurrences("tags", "go"); got != 1 {
		t.Errorf("after delete, Occurrences(tags, go) = %d, want 1", got)
	}
	if got := ix.Contains("lang", "go"); !reflect.DeepEqual(got, []string{"d2"}) {
		t.Errorf("after delete, Contains(lang, go) = %v, want [d2]", got)
	}
}

func TestSameDocumentUpdateReplacesOldOccurrences(t *testing.T) {
	ix := New()
	ix.Upsert(Document{ID: "d1", Fields: map[string][]string{
		"tags": {"a", "a", "b"},
	}})
	ix.Upsert(Document{ID: "d1", Fields: map[string][]string{
		"tags": {"c"},
		"lang": {"a"},
	}})

	final := []Document{{ID: "d1", Fields: map[string][]string{
		"tags": {"c"},
		"lang": {"a"},
	}}}
	verifyAgainstScan(t, ix, final, []string{"a", "b", "c"})

	if got := ix.Occurrences("tags", "a"); got != 0 {
		t.Errorf("stale occurrence: Occurrences(tags, a) = %d, want 0", got)
	}
	if got := ix.Contains("tags", "b"); len(got) != 0 {
		t.Errorf("stale match: Contains(tags, b) = %v, want []", got)
	}
}

func TestReplaceAllPublishesBatch(t *testing.T) {
	ix := New()
	for _, doc := range fixtureDocs() {
		ix.Upsert(doc)
	}

	batch := []Document{
		{ID: "n1", Fields: map[string][]string{"tags": {"x", "x"}}},
		{ID: "n2", Fields: map[string][]string{"tags": {}}},
		// Same document updated twice within one batch: last write wins.
		{ID: "n3", Fields: map[string][]string{"tags": {"old", "old"}}},
		{ID: "n3", Fields: map[string][]string{"tags": {"new"}}},
	}
	ix.ReplaceAll(batch)

	// Verify against a full scan of the batch with n3's last version.
	final := []Document{batch[0], batch[1], batch[3]}
	verifyAgainstScan(t, ix, final, []string{"go", "old", "new", "x"})

	if got := ix.DocumentIDs(); !reflect.DeepEqual(got, []string{"n1", "n2", "n3"}) {
		t.Errorf("DocumentIDs = %v, want [n1 n2 n3]", got)
	}
	if got := ix.Occurrences("tags", "old"); got != 0 {
		t.Errorf("stale batch occurrence: Occurrences(tags, old) = %d, want 0", got)
	}
}

func TestReplaceAllIsAtomicForConcurrentReaders(t *testing.T) {
	buildBatch := func(prefix string, n int) []Document {
		docs := make([]Document, n)
		for i := range docs {
			docs[i] = Document{ID: fmt.Sprintf("%s-%d", prefix, i), Fields: map[string][]string{
				"tags": {prefix, "shared"},
			}}
		}
		return docs
	}
	oldBatch := buildBatch("old", 20)
	newBatch := buildBatch("new", 20)

	ix := New()
	ix.ReplaceAll(oldBatch)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ids := ix.Contains("tags", "shared")
				// The reader must see a consistent generation: all 20 IDs
				// must carry the same prefix, never a mixture.
				prefix := ""
				for _, id := range ids {
					p := id[:3]
					if prefix == "" {
						prefix = p
					} else if p != prefix {
						t.Errorf("observed mixed generations: %v", ids)
						return
					}
				}
				if len(ids) != 20 {
					t.Errorf("observed partial batch: got %d docs, want 20", len(ids))
					return
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		ix.ReplaceAll(newBatch)
		ix.ReplaceAll(oldBatch)
	}
	close(stop)
	wg.Wait()
}

func TestEmptyAndNilFields(t *testing.T) {
	ix := New()
	docs := []Document{
		{ID: "e1", Fields: map[string][]string{"tags": {}}},
		{ID: "e2", Fields: map[string][]string{"tags": nil}},
		{ID: "e3", Fields: nil},
	}
	for _, doc := range docs {
		ix.Upsert(doc)
	}
	verifyAgainstScan(t, ix, docs, []string{"anything"})

	if got := ix.DocumentIDs(); !reflect.DeepEqual(got, []string{"e1", "e2", "e3"}) {
		t.Errorf("DocumentIDs = %v, want [e1 e2 e3]", got)
	}
	if got := ix.Contains("tags", "anything"); len(got) != 0 {
		t.Errorf("Contains on empty arrays = %v, want []", got)
	}

	ix.ReplaceAll(nil)
	if got := ix.DocumentIDs(); len(got) != 0 {
		t.Errorf("after ReplaceAll(nil), DocumentIDs = %v, want []", got)
	}
}
