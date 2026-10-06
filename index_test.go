package prefixdict

import (
	"container/heap"
	"testing"
)

// A batch that lowers the leader's weight must not leave the stale cached
// score in charge: the runner-up is promoted.
func TestStaleCachedScoreInvalidatedRunnerUpPromoted(t *testing.T) {
	d := New()
	d.ReplaceSource("s", map[string]int64{"apple": 100, "apricot": 60, "app": 40})
	assertTopK(t, d.TopK("ap", 3), ws("apple", 100), ws("apricot", 60), ws("app", 40))

	// Winner's weight drops below the runner-up. The batch leaves the
	// cached layer stale on purpose.
	d.ReplaceSource("s", map[string]int64{"apple": 45, "apricot": 60, "app": 40})

	d.mu.RLock()
	ix := d.idx
	d.mu.RUnlock()
	ix.mu.RLock()
	stale := ix.cached["apple"]
	dirty := ix.dirty["apple"]
	ix.mu.RUnlock()
	if stale != 100 || !dirty {
		t.Fatalf("expected apple cached stale at 100 and dirty, got %d dirty=%v", stale, dirty)
	}

	// The query invalidates the stale score: apricot is promoted.
	assertTopK(t, d.TopK("ap", 3), ws("apricot", 60), ws("apple", 45), ws("app", 40))
}

// The heap itself revalidates popped entries against authoritative scores.
func TestDrainTopKRevalidatesStaleEntries(t *testing.T) {
	truth := map[string]int64{"a": 50, "b": 60, "c": 40} // a dropped 100 -> 50
	fresh := func(w string) (int64, bool) { sc, ok := truth[w]; return sc, ok }

	h := newCandHeap([]candidate{
		{word: "a", score: 100}, // stale, inflated
		{word: "b", score: 60},
		{word: "c", score: 40},
	})
	assertTopK(t, drainTopK(h, 3, fresh), ws("b", 60), ws("a", 50), ws("c", 40))
}

// Entries whose word vanished mid-query are dropped, not returned.
func TestDrainTopKDropsRemovedWords(t *testing.T) {
	truth := map[string]int64{"b": 60}
	fresh := func(w string) (int64, bool) { sc, ok := truth[w]; return sc, ok }

	h := newCandHeap([]candidate{{word: "a", score: 100}, {word: "b", score: 60}})
	assertTopK(t, drainTopK(h, 2, fresh), ws("b", 60))
}

// A word whose weight rises while its entry sits in the heap is corrected
// before it can be emitted.
func TestDrainTopKRepushOnIncrease(t *testing.T) {
	truth := map[string]int64{"a": 10, "b": 95} // b rose 5 -> 95
	fresh := func(w string) (int64, bool) { sc, ok := truth[w]; return sc, ok }

	h := &candHeap{}
	heap.Push(h, candidate{word: "a", score: 10})
	heap.Push(h, candidate{word: "b", score: 5}) // stale, understated
	got := drainTopK(h, 2, fresh)
	assertTopK(t, got, ws("a", 10), ws("b", 95))
}

// Writes committed while a rebuild is in flight must be caught up by Finish.
func TestRebuildCatchUpDelta(t *testing.T) {
	d := New()
	d.ReplaceSource("a", map[string]int64{"apple": 100, "apricot": 60})
	d.ReplaceSource("b", map[string]int64{"banana": 5})

	r := d.StartRebuild() // background build starts here

	// Interleaved batches: winner drops, a new word appears, a source leaves.
	d.ReplaceSource("a", map[string]int64{"apple": 45, "apricot": 60})
	d.ReplaceSource("c", map[string]int64{"app": 70})
	d.RemoveSource("b")

	r.Finish() // swap must replay the delta, not serve the stale build

	assertTopK(t, d.TopK("ap", 5), ws("app", 70), ws("apricot", 60), ws("apple", 45))
	assertTopK(t, d.TopK("", 5), ws("app", 70), ws("apricot", 60), ws("apple", 45))
}

// Overlapping rebuilds each catch up to their own swap point; the last
// finisher wins and stays correct.
func TestOverlappingRebuilds(t *testing.T) {
	d := New()
	d.ReplaceSource("a", map[string]int64{"x": 1})

	r1 := d.StartRebuild()
	d.ReplaceSource("a", map[string]int64{"x": 2, "y": 3})
	r2 := d.StartRebuild()
	d.ReplaceSource("b", map[string]int64{"z": 9})

	r1.Finish()
	assertTopK(t, d.TopK("", 3), ws("z", 9), ws("y", 3), ws("x", 2))
	r2.Finish()
	assertTopK(t, d.TopK("", 3), ws("z", 9), ws("y", 3), ws("x", 2))
}

// A snapshot taken before a rebuild keeps serving its own weights after
// the new index is swapped in.
func TestSnapshotUnaffectedByRebuildSwap(t *testing.T) {
	d := New()
	d.ReplaceSource("a", map[string]int64{"apple": 8})
	snap := d.Snapshot()

	r := d.StartRebuild()
	d.ReplaceSource("a", map[string]int64{"apple": 1})
	r.Finish()

	assertTopK(t, snap.TopK("ap", 1), ws("apple", 8))
	assertTopK(t, d.TopK("ap", 1), ws("apple", 1))
}
