package prefixdict

import (
	"container/heap"
	"sort"
)

// index is the published prefix index. The word list is maintained
// synchronously by every write; scores are refreshed lazily (stale marks)
// or in bulk by a background rebuild.
type index struct {
	words  []string         // all existing words, UTF-8 byte order
	scores map[string]int64 // cached totals, possibly stale
	stale  map[string]bool  // words whose cached score cannot be trusted
}

func newIndex() *index {
	return &index{
		scores: make(map[string]int64),
		stale:  make(map[string]bool),
	}
}

func (ix *index) clone() *index {
	words := make([]string, len(ix.words))
	copy(words, ix.words)
	scores := make(map[string]int64, len(ix.scores))
	for word, score := range ix.scores {
		scores[word] = score
	}
	stale := make(map[string]bool, len(ix.stale))
	for word := range ix.stale {
		stale[word] = true
	}
	return &index{words: words, scores: scores, stale: stale}
}

func (ix *index) addWord(word string) {
	i := sort.SearchStrings(ix.words, word)
	if i < len(ix.words) && ix.words[i] == word {
		return
	}
	ix.words = append(ix.words, "")
	copy(ix.words[i+1:], ix.words[i:])
	ix.words[i] = word
	if _, ok := ix.scores[word]; !ok {
		ix.scores[word] = 0
	}
	ix.stale[word] = true
}

func (ix *index) removeWord(word string) {
	i := sort.SearchStrings(ix.words, word)
	if i < len(ix.words) && ix.words[i] == word {
		ix.words = append(ix.words[:i], ix.words[i+1:]...)
	}
	delete(ix.scores, word)
	delete(ix.stale, word)
}

// prefixRange returns the [lo, hi) bounds of words carrying prefix inside a
// byte-ordered word list.
func prefixRange(words []string, prefix string) (int, int) {
	lo := sort.SearchStrings(words, prefix)
	upper, ok := prefixUpperBound(prefix)
	if !ok {
		return lo, len(words)
	}
	return lo, sort.SearchStrings(words, upper)
}

// prefixUpperBound returns the smallest string greater than every string
// that starts with prefix, or false if there is none.
func prefixUpperBound(prefix string) (string, bool) {
	b := []byte(prefix)
	for len(b) > 0 && b[len(b)-1] == 0xff {
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return "", false
	}
	b[len(b)-1]++
	return string(b), true
}

// candidate is a word with its (already validated) score in the heap.
type candidate struct {
	word  string
	score int64
}

// candHeap pops the best candidate first: highest score, ties broken by
// UTF-8 byte order of the word.
type candHeap []candidate

func (h candHeap) Len() int { return len(h) }
func (h candHeap) Less(i, j int) bool {
	if h[i].score != h[j].score {
		return h[i].score > h[j].score
	}
	return h[i].word < h[j].word
}
func (h candHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *candHeap) Push(x interface{}) { *h = append(*h, x.(candidate)) }
func (h *candHeap) Pop() interface{} {
	old := *h
	n := len(old)
	c := old[n-1]
	*h = old[:n-1]
	return c
}

func topKFromHeap(cands candHeap, k int) []Entry {
	heap.Init(&cands)
	out := make([]Entry, 0, k)
	for len(out) < k && cands.Len() > 0 {
		c := heap.Pop(&cands).(candidate)
		out = append(out, Entry{Word: c.word, Weight: c.score})
	}
	return out
}

// Rebuild is an in-flight background index rebuild. The expensive
// aggregation runs on a private copy without holding the DB lock; Finish
// swaps the fresh scores in and backfills the delta of writes that landed
// after the rebuild started.
type Rebuild struct {
	db   *DB
	base uint64
	data map[string]map[string]int64
}

// StartRebuild snapshots the current state for a background rebuild.
// Writes may proceed concurrently; they are not lost.
func (db *DB) StartRebuild() *Rebuild {
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
	return &Rebuild{db: db, base: db.version, data: data}
}

// Finish aggregates scores off-lock, then under the write lock recomputes
// exactly the words mutated since StartRebuild (the delta) and publishes
// the result. The word list itself is always current because writers
// maintain it synchronously.
func (r *Rebuild) Finish() {
	scores := make(map[string]int64, len(r.data))
	for word, srcs := range r.data {
		var total int64
		for _, weight := range srcs {
			total += weight
		}
		scores[word] = total
	}
	r.db.mu.Lock()
	defer r.db.mu.Unlock()
	for word, gen := range r.db.dirtyGen {
		if gen <= r.base {
			continue
		}
		srcs, ok := r.db.data[word]
		if !ok {
			delete(scores, word)
			continue
		}
		var total int64
		for _, weight := range srcs {
			total += weight
		}
		scores[word] = total
	}
	r.db.idx.scores = scores
	r.db.idx.stale = make(map[string]bool)
}

// RebuildAsync runs a full background rebuild and closes the returned
// channel when the new index is published.
func (db *DB) RebuildAsync() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		r := db.StartRebuild()
		r.Finish()
		close(done)
	}()
	return done
}
