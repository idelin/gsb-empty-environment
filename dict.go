// Package prefixdict implements a multi-source weighted dictionary with
// prefix top-k queries.
//
// Each word accumulates integer weights from multiple named sources.
// A word exists as long as at least one source contributes to it, even if
// its total weight is zero; it disappears only when its last source is
// removed. Sources are replaced atomically in batches. Queries run against
// immutable snapshots, so a query holding an old snapshot keeps seeing the
// old weights. The prefix index is rebuilt in the background; swapping in
// the rebuilt index backfills the delta of writes that raced the rebuild.
package prefixdict

import "sync"

// Entry is a word together with its aggregated total weight.
type Entry struct {
	Word   string
	Weight int64
}

// DB is a concurrent-safe weighted dictionary.
type DB struct {
	mu       sync.RWMutex
	version  uint64
	data     map[string]map[string]int64 // word -> source -> weight
	bySource map[string]map[string]int64 // source -> word -> weight
	idx      *index
	dirtyGen map[string]uint64 // word -> version of its last mutation
}

// NewDB returns an empty dictionary.
func NewDB() *DB {
	return &DB{
		data:     make(map[string]map[string]int64),
		bySource: make(map[string]map[string]int64),
		idx:      newIndex(),
		dirtyGen: make(map[string]uint64),
	}
}

// Set upserts a single (source, word) contribution. Repeated updates from
// the same source for the same word replace, not accumulate.
func (db *DB) Set(source, word string, weight int64) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.setLocked(source, word, weight)
}

func (db *DB) setLocked(source, word string, weight int64) {
	srcs, ok := db.data[word]
	if !ok {
		srcs = make(map[string]int64)
		db.data[word] = srcs
		db.idx.addWord(word)
	}
	srcs[source] = weight
	words, ok := db.bySource[source]
	if !ok {
		words = make(map[string]int64)
		db.bySource[source] = words
	}
	words[word] = weight
	db.bump(word)
}

// ReplaceSource atomically replaces every contribution of a source with the
// given batch. An empty batch is equivalent to RemoveSource.
func (db *DB) ReplaceSource(source string, contribs map[string]int64) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.removeSourceLocked(source)
	for word, weight := range contribs {
		db.setLocked(source, word, weight)
	}
}

// RemoveSource withdraws only this source's contributions. Words keep any
// weight contributed by other sources; a word vanishes only when its last
// source is removed.
func (db *DB) RemoveSource(source string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.removeSourceLocked(source)
}

func (db *DB) removeSourceLocked(source string) {
	for word := range db.bySource[source] {
		srcs := db.data[word]
		delete(srcs, source)
		if len(srcs) == 0 {
			delete(db.data, word)
			db.idx.removeWord(word)
		}
		db.bump(word)
	}
	delete(db.bySource, source)
}

func (db *DB) bump(word string) {
	db.version++
	db.dirtyGen[word] = db.version
	db.idx.stale[word] = true
}

// Snapshot captures the current state. The snapshot is immutable and
// detached: later writes never change what queries on it return.
func (db *DB) Snapshot() *Snapshot {
	db.mu.RLock()
	defer db.mu.RUnlock()
	data := make(map[string]map[string]int64, len(db.data))
	for word, srcs := range db.data {
		cp := make(map[string]int64, len(srcs))
		for source, weight := range srcs {
			cp[source] = weight
		}
		data[word] = cp
	}
	return &Snapshot{data: data, idx: db.idx.clone()}
}

// TopK is shorthand for Snapshot().TopK(prefix, k).
func (db *DB) TopK(prefix string, k int) []Entry {
	return db.Snapshot().TopK(prefix, k)
}

// Snapshot is an immutable view of the dictionary at one point in time.
type Snapshot struct {
	data map[string]map[string]int64
	idx  *index // private clone; safe to revalidate in place
}

// total sums the authoritative per-source weights of a word.
func (s *Snapshot) total(word string) int64 {
	var sum int64
	for _, weight := range s.data[word] {
		sum += weight
	}
	return sum
}

// TopK returns the up-to-k highest-total-weight existing words starting
// with prefix. Ties break by UTF-8 byte order of the word. Words with a
// zero total are still returned while they exist.
func (s *Snapshot) TopK(prefix string, k int) []Entry {
	if k <= 0 {
		return nil
	}
	lo, hi := prefixRange(s.idx.words, prefix)
	cands := make(candHeap, 0, hi-lo)
	for _, word := range s.idx.words[lo:hi] {
		score, ok := s.idx.scores[word]
		if !ok || s.idx.stale[word] {
			// Invalidate the stale index score against the
			// authoritative snapshot data before it may win.
			score = s.total(word)
			s.idx.scores[word] = score
			delete(s.idx.stale, word)
		}
		cands = append(cands, candidate{word: word, score: score})
	}
	return topKFromHeap(cands, k)
}
