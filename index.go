// Package docindex implements an in-memory inverted index over documents
// whose fields hold arrays of tokens.
//
// Semantics:
//   - Contains queries use set semantics: a document matches a term at most
//     once, no matter how many times the term repeats in the field array.
//   - Occurrences aggregation counts repetitions: every appearance of a term
//     in a field array adds to the total.
//   - Deleting a document withdraws all of its occurrences.
//   - ReplaceAll publishes a batch atomically: readers observe either the
//     full old index or the full new index, never a mixture.
package docindex

import (
	"sort"
	"sync"
)

// Document is a single indexed document. Each field maps to an array of
// tokens; the array may be empty and may contain repeated tokens.
type Document struct {
	ID     string
	Fields map[string][]string
}

// postings maps field -> term -> docID -> occurrence count.
type postings map[string]map[string]map[string]int

type state struct {
	docs map[string]Document
	post postings
}

// Index is a concurrency-safe inverted index.
type Index struct {
	mu sync.RWMutex
	st *state
}

// New returns an empty index.
func New() *Index {
	return &Index{st: newState()}
}

func newState() *state {
	return &state{
		docs: make(map[string]Document),
		post: make(postings),
	}
}

func (s *state) add(doc Document) {
	s.docs[doc.ID] = doc
	for field, tokens := range doc.Fields {
		terms, ok := s.post[field]
		if !ok {
			terms = make(map[string]map[string]int)
			s.post[field] = terms
		}
		for _, tok := range tokens {
			bucket, ok := terms[tok]
			if !ok {
				bucket = make(map[string]int)
				terms[tok] = bucket
			}
			bucket[doc.ID]++
		}
	}
}

// remove withdraws every occurrence contributed by the document.
func (s *state) remove(id string) {
	doc, ok := s.docs[id]
	if !ok {
		return
	}
	for field, tokens := range doc.Fields {
		terms := s.post[field]
		for _, tok := range tokens {
			bucket := terms[tok]
			bucket[id]--
			if bucket[id] <= 0 {
				delete(bucket, id)
			}
			if len(bucket) == 0 {
				delete(terms, tok)
			}
		}
		if len(terms) == 0 {
			delete(s.post, field)
		}
	}
	delete(s.docs, id)
}

// Upsert inserts or replaces a document. Replacing a document first
// withdraws all occurrences of its previous version.
func (ix *Index) Upsert(doc Document) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.st.remove(doc.ID)
	ix.st.add(doc)
}

// Delete removes a document and all of its occurrences. It is a no-op for
// unknown IDs.
func (ix *Index) Delete(id string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.st.remove(id)
}

// ReplaceAll atomically publishes a batch of documents as the entire new
// index content. The new state is fully built before the swap, so concurrent
// readers never observe a partially applied batch. If the batch contains the
// same document ID more than once, the last one wins.
func (ix *Index) ReplaceAll(docs []Document) {
	next := newState()
	for _, doc := range docs {
		next.remove(doc.ID) // last write wins for duplicate IDs in the batch
		next.add(doc)
	}
	ix.mu.Lock()
	ix.st = next
	ix.mu.Unlock()
}

// Contains returns the sorted IDs of documents whose field contains the
// term at least once (set semantics: duplicates do not multiply matches).
func (ix *Index) Contains(field, term string) []string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	terms := ix.st.post[field]
	bucket := terms[term]
	ids := make([]string, 0, len(bucket))
	for id := range bucket {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Occurrences returns the total number of times the term appears in the
// field across all documents, counting repetitions.
func (ix *Index) Occurrences(field, term string) int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	total := 0
	for _, n := range ix.st.post[field][term] {
		total += n
	}
	return total
}

// DocumentIDs returns the sorted IDs of all documents in the index.
func (ix *Index) DocumentIDs() []string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	ids := make([]string, 0, len(ix.st.docs))
	for id := range ix.st.docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Snapshot returns a deep copy of all documents, keyed by ID. Intended for
// verification against a full document scan.
func (ix *Index) Snapshot() map[string]Document {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make(map[string]Document, len(ix.st.docs))
	for id, doc := range ix.st.docs {
		fields := make(map[string][]string, len(doc.Fields))
		for field, tokens := range doc.Fields {
			cp := make([]string, len(tokens))
			copy(cp, tokens)
			fields[field] = cp
		}
		out[id] = Document{ID: doc.ID, Fields: fields}
	}
	return out
}
