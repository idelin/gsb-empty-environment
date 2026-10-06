package prefixdict

import (
	"container/heap"
	"sort"
	"strings"
	"sync"
)

// index is the live prefix top-k structure: a byte-ordered word list plus
// two score layers.
//
// scores is authoritative and is updated by every committed batch. cached
// is a denormalized copy that batches deliberately leave stale (the word is
// flagged dirty instead), so queries must invalidate stale cached scores
// when seeding their candidate heap. This mirrors a rebuilt index whose
// cached scores lag the deltas replayed onto it.
//
// Invariant: for every word not flagged dirty, cached[word] == scores[word].
type index struct {
	mu     sync.RWMutex
	words  []string         // sorted by UTF-8 byte order
	scores map[string]int64 // authoritative totals
	cached map[string]int64 // denormalized scores, stale for dirty words
	dirty  map[string]bool
}

func newIndex() *index {
	return &index{
		scores: make(map[string]int64),
		cached: make(map[string]int64),
		dirty:  make(map[string]bool),
	}
}

// buildIndex constructs a fully consistent index from a totals snapshot.
func buildIndex(totals map[string]int64) *index {
	ix := newIndex()
	ix.words = make([]string, 0, len(totals))
	for w, sc := range totals {
		ix.words = append(ix.words, w)
		ix.scores[w] = sc
		ix.cached[w] = sc
	}
	sort.Strings(ix.words)
	return ix
}

// applyDelta folds one word update into the index. Score changes mark the
// word dirty and leave the cached layer stale on purpose; membership
// changes keep the sorted word list exact.
func (ix *index) applyDelta(word string, score int64, present bool) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if !present {
		if i, ok := ix.locate(word); ok {
			ix.words = append(ix.words[:i], ix.words[i+1:]...)
		}
		delete(ix.scores, word)
		delete(ix.cached, word)
		delete(ix.dirty, word)
		return
	}
	if _, exists := ix.scores[word]; !exists {
		i, _ := ix.locate(word)
		ix.words = append(ix.words, "")
		copy(ix.words[i+1:], ix.words[i:])
		ix.words[i] = word
		ix.cached[word] = score // nothing stale yet for a brand-new word
	} else if ix.scores[word] != score {
		ix.dirty[word] = true // cached[word] is now stale on purpose
	}
	ix.scores[word] = score
}

// locate finds word's position in the sorted list; ok reports presence.
func (ix *index) locate(word string) (i int, ok bool) {
	i = sort.SearchStrings(ix.words, word)
	return i, i < len(ix.words) && ix.words[i] == word
}

// sortedWords returns a copy of the sorted word list.
func (ix *index) sortedWords() []string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	out := make([]string, len(ix.words))
	copy(out, ix.words)
	return out
}

// topK collects the prefix range, invalidates stale cached scores while
// seeding the candidate heap, then drains the heap with revalidation.
func (ix *index) topK(prefix string, k int) []WordScore {
	if k <= 0 {
		return nil
	}
	ix.mu.RLock()
	lo, hi := prefixRange(ix.words, prefix)
	cands := make([]candidate, 0, hi-lo)
	for _, w := range ix.words[lo:hi] {
		sc := ix.cached[w]
		if ix.dirty[w] {
			// The cached score is stale; seed with the authoritative
			// one instead of trusting the heap entry.
			sc = ix.scores[w]
		}
		cands = append(cands, candidate{word: w, score: sc})
	}
	ix.mu.RUnlock()
	fresh := func(w string) (int64, bool) {
		ix.mu.RLock()
		defer ix.mu.RUnlock()
		sc, ok := ix.scores[w]
		return sc, ok
	}
	return drainTopK(newCandHeap(cands), k, fresh)
}

// prefixRange returns the [lo, hi) bounds of the contiguous run of words
// carrying prefix inside a byte-ordered word list.
func prefixRange(words []string, prefix string) (lo, hi int) {
	lo = sort.SearchStrings(words, prefix)
	hi = lo
	for hi < len(words) && strings.HasPrefix(words[hi], prefix) {
		hi++
	}
	return lo, hi
}

// candidate is one heap entry: a word and the score observed when the entry
// was pushed, which a concurrent batch may already have made stale.
type candidate struct {
	word  string
	score int64
}

// candHeap is a max-heap by score; ties pop the bytewise-smaller word
// first, matching the UTF-8 byte-order tie-break.
type candHeap struct{ items []candidate }

func newCandHeap(cands []candidate) *candHeap {
	h := &candHeap{items: cands}
	heap.Init(h)
	return h
}

func (h *candHeap) Len() int { return len(h.items) }

func (h *candHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.score != b.score {
		return a.score > b.score
	}
	return a.word < b.word
}

func (h *candHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *candHeap) Push(x any) { h.items = append(h.items, x.(candidate)) }

func (h *candHeap) Pop() any {
	old := h.items
	n := len(old)
	top := old[n-1]
	h.items = old[:n-1]
	return top
}

// drainTopK pops up to k results. Every popped entry is revalidated against
// fresh, the authoritative score lookup: an entry whose score went stale
// since it was pushed is re-pushed with its fresh score (so a winner whose
// weight dropped yields to the runner-up), and an entry whose word was
// removed is discarded.
func drainTopK(h *candHeap, k int, fresh func(word string) (int64, bool)) []WordScore {
	out := make([]WordScore, 0, k)
	for len(out) < k && h.Len() > 0 {
		top := heap.Pop(h).(candidate)
		cur, ok := fresh(top.word)
		if !ok {
			continue
		}
		if cur != top.score {
			heap.Push(h, candidate{word: top.word, score: cur})
			continue
		}
		out = append(out, WordScore{Word: top.word, Score: top.score})
	}
	return out
}
