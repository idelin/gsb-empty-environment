// Package docindex implements an in-memory document index where document
// fields may hold arrays of terms.
//
// Semantics:
//   - Contains queries use set semantics: a document matches a term if the
//     term occurs at least once in the field, regardless of duplicates.
//   - Occurrence aggregations count every repetition of a term.
//   - Deleting a document withdraws all of its occurrences.
//   - BatchReplace builds a full replacement snapshot and publishes it
//     atomically; readers never observe a partially applied batch.
package docindex

import (
	"fmt"
	"sort"
	"sync"
)

// Document is an indexed document. Field values are arrays and may be
// empty or contain duplicate terms.
type Document struct {
	ID     string
	Fields map[string][]string
}

// snapshot is an immutable view of the index: the source documents plus
// the inverted counts derived from them.
type snapshot struct {
	docs map[string]Document
	// inverted[field][term][docID] = number of occurrences of term in
	// the field of that document.
	inverted map[string]map[string]map[string]int
}

func buildSnapshot(docs []Document) *snapshot {
	s := &snapshot{
		docs:     make(map[string]Document, len(docs)),
		inverted: make(map[string]map[string]map[string]int),
	}
	for _, doc := range docs {
		s.docs[doc.ID] = doc
		addToInverted(s.inverted, doc)
	}
	return s
}

func addToInverted(inverted map[string]map[string]map[string]int, doc Document) {
	for field, terms := range doc.Fields {
		for _, term := range terms {
			byTerm, ok := inverted[field]
			if !ok {
				byTerm = make(map[string]map[string]int)
				inverted[field] = byTerm
			}
			byDoc, ok := byTerm[term]
			if !ok {
				byDoc = make(map[string]int)
				byTerm[term] = byDoc
			}
			byDoc[doc.ID]++
		}
	}
}

// Index is a concurrency-safe document index.
type Index struct {
	mu   sync.RWMutex
	snap *snapshot
}

// New returns an empty index.
func New() *Index {
	return &Index{snap: buildSnapshot(nil)}
}

// BatchReplace atomically replaces the entire index contents with the
// given documents. The replacement snapshot is fully built before being
// published, so concurrent readers see either the old or the new index,
// never a mix. Later documents with the same ID replace earlier ones.
func (ix *Index) BatchReplace(docs []Document) {
	next := buildSnapshot(docs)
	ix.mu.Lock()
	ix.snap = next
	ix.mu.Unlock()
}

// Delete removes a document and withdraws every occurrence it contributed
// to the inverted counts. It reports whether the document existed.
func (ix *Index) Delete(id string) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	doc, ok := ix.snap.docs[id]
	if !ok {
		return false
	}
	for field, terms := range doc.Fields {
		// Tally duplicates so each occurrence is withdrawn exactly once.
		counts := make(map[string]int, len(terms))
		for _, term := range terms {
			counts[term]++
		}
		for term, n := range counts {
			byDoc := ix.snap.inverted[field][term]
			byDoc[id] -= n
			if byDoc[id] <= 0 {
				delete(byDoc, id)
			}
			if len(byDoc) == 0 {
				delete(ix.snap.inverted[field], term)
			}
		}
		if len(ix.snap.inverted[field]) == 0 {
			delete(ix.snap.inverted, field)
		}
	}
	delete(ix.snap.docs, id)
	return true
}

// Contains returns the sorted IDs of documents whose field contains the
// term at least once (set semantics: duplicates do not change the result).
func (ix *Index) Contains(field, term string) []string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	byDoc := ix.snap.inverted[field][term]
	ids := make([]string, 0, len(byDoc))
	for id := range byDoc {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Occurrence returns the total number of times the term appears in the
// field across all documents, counting duplicates.
func (ix *Index) Occurrence(field, term string) int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	total := 0
	for _, n := range ix.snap.inverted[field][term] {
		total += n
	}
	return total
}

// Document returns a copy of the stored document and whether it exists.
func (ix *Index) Document(id string) (Document, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	doc, ok := ix.snap.docs[id]
	if !ok {
		return Document{}, false
	}
	fields := make(map[string][]string, len(doc.Fields))
	for field, terms := range doc.Fields {
		fields[field] = append([]string(nil), terms...)
	}
	return Document{ID: doc.ID, Fields: fields}, true
}

// Len returns the number of documents in the index.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.snap.docs)
}

// Verify performs a full scan of the stored documents, rebuilds the
// inverted counts from scratch, and reports any discrepancy with the
// maintained index. A nil result means the index is consistent.
func (ix *Index) Verify() []error {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	want := buildSnapshot(nil)
	for _, doc := range ix.snap.docs {
		addToInverted(want.inverted, doc)
	}
	var errs []error
	for field, byTerm := range want.inverted {
		for term, byDoc := range byTerm {
			for id, n := range byDoc {
				if got := ix.snap.inverted[field][term][id]; got != n {
					errs = append(errs, fmt.Errorf(
						"field %q term %q doc %q: index has %d occurrences, documents have %d",
						field, term, id, got, n))
				}
			}
		}
	}
	for field, byTerm := range ix.snap.inverted {
		for term, byDoc := range byTerm {
			for id, n := range byDoc {
				if _, ok := want.inverted[field][term][id]; !ok {
					errs = append(errs, fmt.Errorf(
						"field %q term %q doc %q: index has %d stale occurrences, documents have none",
						field, term, id, n))
				}
			}
		}
	}
	return errs
}

// Occurrences returns the occurrence counts of several terms in one field,
// read atomically under a single lock so the counts are mutually consistent.
func (ix *Index) Occurrences(field string, terms ...string) []int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]int, len(terms))
	for i, term := range terms {
		for _, n := range ix.snap.inverted[field][term] {
			out[i] += n
		}
	}
	return out
}
