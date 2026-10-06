// Package prefixdict implements a multi-source weighted dictionary with
// prefix top-k queries.
//
// Each word may carry integer weights from multiple sources; its score is
// the sum of all per-source weights. Sources are replaced atomically as a
// whole batch: ReplaceSource swaps the source's entire contribution in one
// critical section, and RemoveSource revokes only that source's
// contribution. A word exists while at least one source contributes to it,
// even when its total weight is zero; it disappears only when its last
// contributing source is removed.
//
// TopK returns the k highest-scored words sharing a prefix, ordered by
// score descending with ties broken by UTF-8 byte order of the word.
//
// Snapshot captures an immutable view: queries on an old snapshot keep
// seeing the old weights even after newer batches are applied.
//
// The live top-k index can be rebuilt in the background (StartRebuild /
// Finish). Writes that land while a rebuild is in flight are recorded as
// deltas and replayed onto the new index before it is swapped in, so no
// update is lost across the switch.
package prefixdict

import "sync"

// WordScore pairs a word with its aggregated weight.
type WordScore struct {
	Word  string
	Score int64
}

// delta records the authoritative state of one word after a write, used to
// catch a freshly rebuilt index up with writes that landed during the build.
type delta struct {
	version uint64
	word    string
	score   int64
	present bool // false: the word lost its last source and must be removed
}

// Dictionary is the live, mutable store. It is safe for concurrent use.
type Dictionary struct {
	mu      sync.RWMutex
	sources map[string]map[string]int64 // source -> word -> weight
	totals  map[string]int64            // word -> aggregated weight (presence == existence)
	contrib map[string]int              // word -> number of contributing sources

	idx     *index
	version uint64
	deltas  []delta // writes since the current index generation

	activeRebuilds int
}

// New returns an empty dictionary.
func New() *Dictionary {
	return &Dictionary{
		sources: make(map[string]map[string]int64),
		totals:  make(map[string]int64),
		contrib: make(map[string]int),
		idx:     newIndex(),
	}
}

// ReplaceSource atomically replaces the whole batch contributed by source.
// Weights from the previous batch of the same source stop counting; other
// sources are untouched. Repeating a source never accumulates.
func (d *Dictionary) ReplaceSource(source string, batch map[string]int64) {
	next := make(map[string]int64, len(batch))
	for w, wt := range batch {
		next[w] = wt
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	prev := d.sources[source]
	d.sources[source] = next

	affected := make(map[string]struct{}, len(prev)+len(next))
	for w, oldWt := range prev {
		if _, ok := next[w]; !ok {
			d.totals[w] -= oldWt
			d.contrib[w]--
			affected[w] = struct{}{}
		}
	}
	for w, newWt := range next {
		oldWt := prev[w]
		d.totals[w] += newWt - oldWt
		if _, ok := prev[w]; !ok {
			d.contrib[w]++
		}
		affected[w] = struct{}{}
	}
	d.commitLocked(affected)
}

// RemoveSource revokes only this source's contribution. Words keep existing
// (with their remaining, possibly zero, total) until their last source is
// removed.
func (d *Dictionary) RemoveSource(source string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	prev, ok := d.sources[source]
	if !ok {
		return
	}
	delete(d.sources, source)

	affected := make(map[string]struct{}, len(prev))
	for w, oldWt := range prev {
		d.totals[w] -= oldWt
		d.contrib[w]--
		affected[w] = struct{}{}
	}
	d.commitLocked(affected)
}

// commitLocked folds the affected words into totals, the delta log and the
// live index. Callers must hold d.mu.
func (d *Dictionary) commitLocked(affected map[string]struct{}) {
	if len(affected) == 0 {
		return
	}
	d.version++
	batch := make([]delta, 0, len(affected))
	for w := range affected {
		if d.contrib[w] > 0 {
			batch = append(batch, delta{version: d.version, word: w, score: d.totals[w], present: true})
		} else {
			delete(d.totals, w)
			delete(d.contrib, w)
			batch = append(batch, delta{version: d.version, word: w, present: false})
		}
	}
	d.deltas = append(d.deltas, batch...)
	for _, dl := range batch {
		d.idx.applyDelta(dl.word, dl.score, dl.present)
	}
}

// Score reports the current aggregated weight of word and whether the word
// still exists (i.e. has at least one contributing source).
func (d *Dictionary) Score(word string) (int64, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	sc, ok := d.totals[word]
	return sc, ok
}

// TopK returns up to k words starting with prefix, highest aggregated
// weight first; ties break by UTF-8 byte order. It is served by the live
// index and reflects all committed batches.
func (d *Dictionary) TopK(prefix string, k int) []WordScore {
	d.mu.RLock()
	idx := d.idx
	d.mu.RUnlock()
	return idx.topK(prefix, k)
}

// Snapshot returns an immutable view of the dictionary. Queries on it keep
// seeing the weights as of Snapshot() time, regardless of later writes.
func (d *Dictionary) Snapshot() *Snapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	totals := make(map[string]int64, len(d.totals))
	for w, t := range d.totals {
		totals[w] = t
	}
	return &Snapshot{totals: totals, words: d.idx.sortedWords()}
}

// Snapshot is an immutable, consistent view of the dictionary at a point in
// time.
type Snapshot struct {
	totals map[string]int64
	words  []string // sorted by UTF-8 byte order
}

// TopK is the snapshot analogue of Dictionary.TopK: it never observes
// weights committed after the snapshot was taken.
func (s *Snapshot) TopK(prefix string, k int) []WordScore {
	if k <= 0 {
		return nil
	}
	lo, hi := prefixRange(s.words, prefix)
	cands := make([]candidate, 0, hi-lo)
	for _, w := range s.words[lo:hi] {
		cands = append(cands, candidate{word: w, score: s.totals[w]})
	}
	fresh := func(w string) (int64, bool) {
		sc, ok := s.totals[w]
		return sc, ok
	}
	return drainTopK(newCandHeap(cands), k, fresh)
}

// Rebuild is an in-flight background index rebuild. The expensive build
// happens outside the dictionary lock; Finish replays every write committed
// since StartRebuild onto the new index before it is swapped in.
type Rebuild struct {
	d    *Dictionary
	base uint64
	idx  *index
	done bool
}

// StartRebuild snapshots the current totals and builds a fresh index from
// them without blocking writers. Call Finish to atomically swap it in.
func (d *Dictionary) StartRebuild() *Rebuild {
	d.mu.Lock()
	d.activeRebuilds++
	base := d.version
	totals := make(map[string]int64, len(d.totals))
	for w, t := range d.totals {
		totals[w] = t
	}
	d.mu.Unlock()
	return &Rebuild{d: d, base: base, idx: buildIndex(totals)}
}

// Finish catches the rebuilt index up with every batch committed since
// StartRebuild (the delta) and atomically installs it.
func (r *Rebuild) Finish() {
	if r.done {
		return
	}
	d := r.d
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, dl := range d.deltas {
		if dl.version > r.base {
			r.idx.applyDelta(dl.word, dl.score, dl.present)
		}
	}
	d.idx = r.idx
	d.activeRebuilds--
	if d.activeRebuilds == 0 {
		// The swapped-in index is current; no in-flight rebuild can still
		// need the accumulated deltas.
		d.deltas = nil
	}
	r.done = true
}

// RebuildIndex rebuilds and swaps the index in one call.
func (d *Dictionary) RebuildIndex() {
	d.StartRebuild().Finish()
}
