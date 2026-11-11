package evidence

import (
	"bytes"
	"container/heap"
	"sort"
	"sync"
)

// block is one non-overlapping, retained byte range of the stream.
type block struct {
	rng  Range
	data []byte
	refs int
}

// candidateState tracks a registered candidate and the blocks backing it.
type candidateState struct {
	cand    Candidate
	window  Range
	blocks  []*block
	evicted bool
	// evictedRanges tracks ranges lost to eviction so later Collect calls keep
	// reporting them instead of mislabeling them as gaps or pending.
	evictedRanges []Range
	index         int // heap index, -1 when not in the eviction heap
}

// evictionHeap orders candidates for budget-driven removal: highest Priority
// value (least important) first, then smallest EventID.
type evictionHeap []*candidateState

func (h evictionHeap) Len() int { return len(h) }
func (h evictionHeap) Less(i, j int) bool {
	if h[i].cand.Priority != h[j].cand.Priority {
		return h[i].cand.Priority > h[j].cand.Priority
	}
	return h[i].cand.EventID < h[j].cand.EventID
}
func (h evictionHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *evictionHeap) Push(x any) {
	c := x.(*candidateState)
	c.index = len(*h)
	*h = append(*h, c)
}
func (h *evictionHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	old[n-1] = nil
	c.index = -1
	*h = old[:n-1]
	return c
}

// Buffer is a concurrency-safe bounded evidence buffer.
type Buffer struct {
	mu sync.Mutex

	budget int64
	// blocks are disjoint, sorted by stream offset and cover exactly the
	// written ranges (after overlap splitting).
	blocks []*block
	// coverage is an independent, merged view of all written offsets; unlike
	// blocks it never shrinks when cached blocks are reclaimed, so gap and
	// pending classification stay stable for the lifetime of the stream.
	coverage []Range

	candidates map[uint64]*candidateState
	evictQ     evictionHeap

	evictions []Eviction
	overflow  int64
	sealed    bool
}

// NewBuffer creates a buffer with the given global byte budget. A non-positive
// budget means no enforced cap; eviction then never runs.
func NewBuffer(budgetBytes int64) *Buffer {
	b := &Buffer{
		budget:     budgetBytes,
		candidates: make(map[uint64]*candidateState),
	}
	b.evictQ = make(evictionHeap, 0)
	heap.Init(&b.evictQ)
	return b
}

// Write stores p at absolute stream offset off. Overlapping writes must carry
// identical bytes; conflicting content returns ErrInvalidRange without mutating
// the buffer. Out-of-order writes are allowed and stream holes are preserved.
func (b *Buffer) Write(off int64, p []byte) error {
	if off < 0 {
		return ErrInvalidRange
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return ErrSealed
	}
	if len(p) == 0 {
		return nil
	}

	incoming := Range{Start: off, End: off + int64(len(p))}
	// Validate overlapping content before mutating anything.
	for _, blk := range b.blocks {
		ov := intersect(incoming, blk.rng)
		if ov.Len() == 0 {
			continue
		}
		old := blk.data[ov.Start-blk.rng.Start : ov.End-blk.rng.Start]
		fresh := p[ov.Start-off : ov.End-off]
		if !bytes.Equal(old, fresh) {
			return ErrInvalidRange
		}
	}

	// Split the write into fragments not already stored.
	frags := splitNewFragments(off, p, b.coverage)
	inserted := make([]*block, 0, len(frags))
	for _, frag := range frags {
		blk := &block{
			rng:  Range{Start: frag.start, End: frag.end},
			data: append([]byte(nil), frag.data...),
		}
		b.insertBlock(blk)
		inserted = append(inserted, blk)
	}
	b.addCoverage(incoming)

	// Newly retained bytes are referenced by every active candidate whose
	// window covers them, so committed windows keep their backing storage.
	for _, cand := range b.candidates {
		if cand.evicted {
			continue
		}
		for _, blk := range inserted {
			if intersect(blk.rng, cand.window).Len() > 0 {
				blk.refs++
				cand.blocks = append(cand.blocks, blk)
			}
		}
	}

	b.enforceBudget()
	return nil
}

// Notify registers a detection candidate and commits retention of its window.
// Bytes already present are pinned immediately; bytes arriving later are pinned
// by Write. The returned error is non-nil for invalid or duplicate candidates.
func (b *Buffer) Notify(c Candidate) error {
	if c.EventID == 0 || c.Match.Len() <= 0 || c.BeforeContext < 0 || c.AfterContext < 0 {
		return ErrInvalidRange
	}
	window := c.Window()
	if window.Len() <= 0 {
		return ErrInvalidRange
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if existing, ok := b.candidates[c.EventID]; ok {
		if existing.evicted {
			return ErrCandidateFinalized
		}
		return ErrDuplicateEvent
	}

	cs := &candidateState{cand: c, window: window, index: -1}
	for _, blk := range b.blocks {
		if intersect(blk.rng, window).Len() > 0 {
			blk.refs++
			cs.blocks = append(cs.blocks, blk)
		}
	}
	b.candidates[c.EventID] = cs
	heap.Push(&b.evictQ, cs)

	b.enforceBudget()
	return nil
}

// Cancel releases the candidate's block references. Reachable ranges become
// reclaimable cache; they are never reported as evicted missing ranges.
func (b *Buffer) Cancel(eventID uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	cs, ok := b.candidates[eventID]
	if !ok {
		return ErrCandidateNotFound
	}
	if cs.evicted {
		return ErrCandidateFinalized
	}
	b.removeCandidate(cs)
	delete(b.candidates, eventID)
	b.enforceBudget()
	return nil
}

// Collect materializes the evidence window. Active candidates return every
// retained byte plus explicit missing segments; evicted candidates return the
// snapshot captured at eviction time.
func (b *Buffer) Collect(eventID uint64) (Evidence, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cs, ok := b.candidates[eventID]
	if !ok {
		return Evidence{}, ErrCandidateNotFound
	}
	ev := Evidence{
		EventID:  eventID,
		Priority: cs.cand.Priority,
		Match:    cs.cand.Match,
		Window:   cs.window,
		Evicted:  cs.evicted,
	}
	if cs.evicted {
		ev.Segments = b.buildEvictedSegments(cs)
	} else {
		ev.Segments = b.buildActiveSegments(cs)
	}
	return ev, nil
}

// Evictions returns a copy of eviction records in eviction order.
func (b *Buffer) Evictions() []Eviction {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Eviction, len(b.evictions))
	for i, e := range b.evictions {
		out[i] = Eviction{
			EventID:  e.EventID,
			Priority: e.Priority,
			Window:   e.Window,
			Missing:  append([]Segment(nil), e.Missing...),
		}
	}
	return out
}

// Close marks the stream complete. Writes after Close fail with ErrSealed;
// ranges that never arrived remain ReasonPending in evidence.
func (b *Buffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sealed = true
}

// Stats returns a point-in-time occupancy snapshot.
func (b *Buffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	var retained, referenced, cached int64
	for _, blk := range b.blocks {
		retained += blk.rng.Len()
		if blk.refs > 0 {
			referenced += blk.rng.Len()
		} else {
			cached += blk.rng.Len()
		}
	}
	active := 0
	for _, cs := range b.candidates {
		if !cs.evicted {
			active++
		}
	}
	return Stats{
		RetainedBytes:     retained,
		ReferencedBytes:   referenced,
		CachedBytes:       cached,
		OverflowBytes:     b.overflow,
		BudgetBytes:       b.budget,
		ActiveCandidates:  active,
		EvictedCandidates: len(b.evictions),
		Sealed:            b.sealed,
	}
}

// enforceBudget restores the budget invariant: referenced bytes for active
// candidates are never truncated. Unreferenced cache is reclaimed first, then
// candidates are evicted in policy order. If even an empty buffer exceeds the
// budget (a single write larger than the budget), overflow is reported instead.
func (b *Buffer) enforceBudget() {
	if b.budget <= 0 {
		return
	}
	for b.retainedBytes() > b.budget {
		if b.reclaimOneCachedBlock() {
			continue
		}
		if b.evictQ.Len() == 0 {
			// Defensive: every retained byte is pinned and no candidate
			// remains to evict, so the cap cannot be restored without
			// corrupting internal accounting; surface it as overflow.
			b.overflow = b.retainedBytes() - b.budget
			return
		}
		victim := heap.Pop(&b.evictQ).(*candidateState)
		b.evictCandidate(victim)
	}
	b.overflow = 0
}

func (b *Buffer) evictCandidate(cs *candidateState) {
	record := Eviction{
		EventID:  cs.cand.EventID,
		Priority: cs.cand.Priority,
		Window:   cs.window,
	}

	retained := b.retainedRangesInWindow(cs.window)
	for _, rng := range retained {
		record.Missing = append(record.Missing, Segment{Range: rng, Reason: ReasonEvicted})
		cs.evictedRanges = append(cs.evictedRanges, rng)
	}

	b.releaseReferences(cs)
	cs.evicted = true
	cs.blocks = nil
	b.evictions = append(b.evictions, record)

	// Reclaim now-unreferenced blocks only while the cap is still exceeded;
	// the rest stays available as pre-context cache for later candidates.
	for b.budget > 0 && b.retainedBytes() > b.budget && b.reclaimOneCachedBlock() {
	}
}

func (b *Buffer) removeCandidate(cs *candidateState) {
	if cs.index >= 0 {
		heap.Remove(&b.evictQ, cs.index)
	}
	b.releaseReferences(cs)
	cs.blocks = nil
}

func (b *Buffer) releaseReferences(cs *candidateState) {
	for _, blk := range cs.blocks {
		blk.refs--
		if blk.refs < 0 {
			panic("evidence: negative block reference count")
		}
	}
}

// reclaimOneCachedBlock drops the oldest unreferenced block.
func (b *Buffer) reclaimOneCachedBlock() bool {
	for i, blk := range b.blocks {
		if blk.refs == 0 {
			b.blocks = append(b.blocks[:i], b.blocks[i+1:]...)
			return true
		}
	}
	return false
}

func (b *Buffer) insertBlock(blk *block) {
	pos := sort.Search(len(b.blocks), func(i int) bool {
		return b.blocks[i].rng.Start >= blk.rng.Start
	})
	b.blocks = append(b.blocks, nil)
	copy(b.blocks[pos+1:], b.blocks[pos:])
	b.blocks[pos] = blk
}

// addCoverage merges rng into the persistent coverage interval set. The set
// is sorted and disjoint; adjacent or overlapping ranges are coalesced.
func (b *Buffer) addCoverage(rng Range) {
	pos := sort.Search(len(b.coverage), func(i int) bool {
		return b.coverage[i].End > rng.Start
	})
	merged := rng
	end := pos
	for end < len(b.coverage) && b.coverage[end].Start <= merged.End {
		if b.coverage[end].Start < merged.Start {
			merged.Start = b.coverage[end].Start
		}
		if b.coverage[end].End > merged.End {
			merged.End = b.coverage[end].End
		}
		end++
	}
	if end == pos {
		b.coverage = append(b.coverage, Range{})
		copy(b.coverage[pos+1:], b.coverage[pos:])
		b.coverage[pos] = merged
		return
	}
	b.coverage[pos] = merged
	copy(b.coverage[pos+1:], b.coverage[end:])
	b.coverage = b.coverage[:len(b.coverage)-(end-pos-1)]
}

func (b *Buffer) retainedBytes() int64 {
	var total int64
	for _, blk := range b.blocks {
		total += blk.rng.Len()
	}
	return total
}

func (b *Buffer) retainedRangesInWindow(window Range) []Range {
	var out []Range
	for _, blk := range b.blocks {
		rng := blk.rng
		if ov := intersect(rng, window); ov.Len() > 0 {
			out = append(out, ov)
		}
	}
	return out
}

// buildActiveSegments walks the window left to right, interleaving retained
// data blocks with explicit missing ranges.
func (b *Buffer) buildActiveSegments(cs *candidateState) []Segment {
	return b.buildSegments(cs, false)
}

// buildSegments tiles the candidate window. Covered offsets come from the
// persistent coverage set; each covered sub-range is served from a live block
// when one still exists, otherwise it is reported as an explicit missing
// segment. With includeEviction true, ranges recorded at eviction time take
// precedence and are labeled ReasonEvicted.
func (b *Buffer) buildSegments(cs *candidateState, includeEviction bool) []Segment {
	segs := make([]Segment, 0, len(b.coverage)+len(b.blocks)+1)
	cursor := cs.window.Start
	blockPos := 0

	for _, covered := range b.coverage {
		ov := intersect(covered, cs.window)
		if ov.Len() == 0 {
			continue
		}
		if ov.Start > cursor {
			segs = b.appendCoveredMissing(segs, cs, Range{Start: cursor, End: ov.Start}, includeEviction)
			cursor = ov.Start
		}

		// Advance the block pointer past blocks that end before this covered
		// sub-range. Both blocks and coverage are kept sorted and disjoint.
		for blockPos < len(b.blocks) && b.blocks[blockPos].rng.End <= ov.Start {
			blockPos++
		}
		partCursor := ov.Start
		for blockPos < len(b.blocks) && b.blocks[blockPos].rng.Start < ov.End {
			blk := b.blocks[blockPos]
			if blk.rng.Start > partCursor {
				segs = b.appendCoveredMissing(segs, cs, Range{Start: partCursor, End: blk.rng.Start}, includeEviction)
				cursor = blk.rng.Start
				partCursor = blk.rng.Start
			}
			held := intersect(blk.rng, ov)
			if held.Len() > 0 {
				data := blk.data[held.Start-blk.rng.Start : held.End-blk.rng.Start]
				segs = append(segs, Segment{
					Range: held,
					Data:  append([]byte(nil), data...),
				})
				partCursor = held.End
				cursor = held.End
			}
			blockPos++
		}
		if partCursor < ov.End {
			segs = b.appendCoveredMissing(segs, cs, Range{Start: partCursor, End: ov.End}, includeEviction)
			cursor = ov.End
		}
	}
	if cursor < cs.window.End {
		segs = b.appendCoveredMissing(segs, cs, Range{Start: cursor, End: cs.window.End}, includeEviction)
	}
	return segs
}

// appendCoveredMissing emits missing segments for rng, honoring eviction
// records when requested.
func (b *Buffer) appendCoveredMissing(segs []Segment, cs *candidateState, rng Range, includeEviction bool) []Segment {
	if !includeEviction || len(cs.evictedRanges) == 0 {
		return append(segs, b.missingSegment(rng))
	}
	return b.appendMissing(segs, cs, rng)
}

// buildEvictedSegments reconstructs the window from the eviction snapshot.
// Ranges lost at eviction time are marked ReasonEvicted; everything else keeps
// its gap or pending classification.
func (b *Buffer) buildEvictedSegments(cs *candidateState) []Segment {
	return b.buildSegments(cs, true)
}

// appendMissing splits rng at every evicted range so eviction marks win over
// gap/pending classification; the returned segments cover rng exactly.
func (b *Buffer) appendMissing(segs []Segment, cs *candidateState, rng Range) []Segment {
	cursor := rng.Start
	for _, evRange := range cs.evictedRanges {
		ov := intersect(rng, evRange)
		if ov.Len() == 0 {
			continue
		}
		if ov.Start > cursor {
			segs = append(segs, b.missingSegment(Range{Start: cursor, End: ov.Start}))
		}
		segs = append(segs, Segment{Range: ov, Reason: ReasonEvicted})
		cursor = ov.End
	}
	if cursor < rng.End {
		segs = append(segs, b.missingSegment(Range{Start: cursor, End: rng.End}))
	}
	return segs
}

// missingSegment classifies a range known to hold no retained bytes.
func (b *Buffer) missingSegment(rng Range) Segment {
	reason := ReasonGap
	// Pending bytes lie outside the offset span ever covered: before the
	// first covered offset or after the last one. Holes between covered
	// ranges are genuine stream gaps, even if a later write jumps ahead.
	if len(b.coverage) == 0 ||
		rng.Start < b.coverage[0].Start ||
		rng.Start >= b.coverage[len(b.coverage)-1].End {
		reason = ReasonPending
	}
	return Segment{Range: rng, Reason: reason}
}
