package lsm

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
)

// ErrCorruption is returned when two records share key and sequence but
// carry different content.
var ErrCorruption = errors.New("lsm: conflicting records with identical key and sequence")

// Coverage abstracts what is known about keys in levels below the
// compaction output. MightContain must be conservative: it may report
// true for absent keys, but never false for a present one.
type Coverage interface {
	MightContain(key string) bool
}

// RangeCoverage proves absence only from segment key ranges. A lower
// segment whose range merely overlaps the key forces a positive answer
// even if the key is not actually present.
type RangeCoverage struct {
	Segments []*Segment
}

func (c RangeCoverage) MightContain(key string) bool {
	for _, s := range c.Segments {
		if s.ContainsRange(key) {
			return true
		}
	}
	return false
}

// ExactCoverage scans lower segments and answers by actual key presence.
type ExactCoverage struct {
	Segments []*Segment
}

func (c ExactCoverage) MightContain(key string) bool {
	for _, s := range c.Segments {
		if s.HasKey(key) {
			return true
		}
	}
	return false
}

// Plan describes a chosen compaction. Building a plan never mutates the
// manifest; the plan is published later via Manifest.Commit.
type Plan struct {
	Level     int   // source level; outputs land in Level+1
	InputIDs  []int // segment IDs from Level and Level+1
	OutputIDs []int // filled by Commit
}

// SelectCompaction picks every segment of level plus the overlapping
// segments of level+1. Selection is pure: the manifest stays untouched
// and readers keep using the old version until Commit.
func SelectCompaction(v *Version, level int) *Plan {
	if level < 0 || level >= len(v.Levels) || len(v.Levels[level]) == 0 {
		return nil
	}
	p := &Plan{Level: level}
	lo, hi := "", ""
	for i, s := range v.Levels[level] {
		p.InputIDs = append(p.InputIDs, s.ID)
		if i == 0 || s.MinKey() < lo {
			lo = s.MinKey()
		}
		if i == 0 || s.MaxKey() > hi {
			hi = s.MaxKey()
		}
	}
	if level+1 < len(v.Levels) {
		for _, s := range v.Levels[level+1] {
			if s.RangeOverlaps(lo, hi) {
				p.InputIDs = append(p.InputIDs, s.ID)
			}
		}
	}
	return p
}

// gatherInputs resolves plan segment IDs against a version.
func gatherInputs(v *Version, p *Plan) ([]*Segment, error) {
	byID := make(map[int]*Segment)
	for _, lvl := range v.Levels {
		for _, s := range lvl {
			byID[s.ID] = s
		}
	}
	var out []*Segment
	for _, id := range p.InputIDs {
		s, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("lsm: plan references unknown segment %d", id)
		}
		out = append(out, s)
	}
	return out, nil
}

// mergeIter is a k-way merge over sorted segments yielding records in
// (key asc, seq desc) order.
type mergeIter struct {
	h   segHeap
	rec Record
	ok  bool
}

type segItem struct {
	seg *Segment
	pos int
}

type segHeap []segItem

func (h segHeap) Len() int { return len(h) }
func (h segHeap) Less(i, j int) bool {
	a, b := h[i].seg.Records[h[i].pos], h[j].seg.Records[h[j].pos]
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	return a.Seq > b.Seq
}
func (h segHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *segHeap) Push(x interface{}) { *h = append(*h, x.(segItem)) }
func (h *segHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func newMergeIter(segs []*Segment) *mergeIter {
	m := &mergeIter{}
	for _, s := range segs {
		if len(s.Records) > 0 {
			m.h = append(m.h, segItem{seg: s})
		}
	}
	heap.Init(&m.h)
	return m
}

// next advances to the next merged record.
func (m *mergeIter) next() bool {
	if len(m.h) == 0 {
		m.ok = false
		return false
	}
	it := heap.Pop(&m.h).(segItem)
	m.rec = it.seg.Records[it.pos]
	m.ok = true
	if it.pos+1 < len(it.seg.Records) {
		heap.Push(&m.h, segItem{seg: it.seg, pos: it.pos + 1})
	}
	return true
}

// Compact merges the input segments into new, key-non-overlapping
// segments of at most maxRecs records (split only at key boundaries).
//
// snapshots holds the pinned sequence numbers of active readers. For
// each key, a version is retained iff it is the newest version or some
// pinned snapshot sees it. A tombstone is dropped only when all of:
//
//  1. it is the oldest retained version of its key (nothing below it in
//     this compaction's output),
//  2. lower levels provably hold no older value (via coverage), and
//  3. no pinned snapshot has seq >= the tombstone's seq.
//
// Being the newest record is never, by itself, grounds for removal.
func Compact(inputs []*Segment, snapshots []uint64, lower Coverage, maxRecs int) ([][]Record, error) {
	snaps := append([]uint64(nil), snapshots...)
	sort.Slice(snaps, func(i, j int) bool { return snaps[i] < snaps[j] })

	if maxRecs <= 0 {
		maxRecs = 1 << 30
	}

	var outputs [][]Record
	var cur []Record
	flush := func() {
		if len(cur) > 0 {
			outputs = append(outputs, cur)
			cur = nil
		}
	}

	it := newMergeIter(inputs)
	var group []Record
	emitGroup := func() error {
		kept, err := gcGroup(group, snaps)
		if err != nil {
			return err
		}
		kept = maybeDropTombstone(kept, snaps, lower)
		if len(cur) >= maxRecs {
			flush() // split only at key boundaries
		}
		cur = append(cur, kept...)
		return nil
	}

	for it.next() {
		r := it.rec
		if len(group) > 0 && group[0].Key != r.Key {
			if err := emitGroup(); err != nil {
				return nil, err
			}
			group = group[:0]
		}
		group = append(group, r)
	}
	if len(group) > 0 {
		if err := emitGroup(); err != nil {
			return nil, err
		}
	}
	flush()
	return outputs, nil
}

// gcGroup deduplicates same-seq records (detecting corruption) and keeps
// exactly the versions visible to some pinned snapshot plus the newest.
// group must be ordered by seq desc for one key.
func gcGroup(group []Record, snaps []uint64) ([]Record, error) {
	dedup := group[:0]
	for _, r := range group {
		if len(dedup) > 0 && dedup[len(dedup)-1].Seq == r.Seq {
			if !sameContent(dedup[len(dedup)-1], r) {
				return nil, fmt.Errorf("%w: key %q seq %d", ErrCorruption, r.Key, r.Seq)
			}
			continue
		}
		dedup = append(dedup, r)
	}

	kept := make([]Record, 0, len(dedup))
	for i, r := range dedup {
		if i == 0 {
			kept = append(kept, r) // newest: needed by current/future reads
			continue
		}
		newer := dedup[i-1].Seq
		// Keep iff some pinned snapshot S satisfies r.Seq <= S < newer.
		idx := sort.Search(len(snaps), func(j int) bool { return snaps[j] >= r.Seq })
		if idx < len(snaps) && snaps[idx] < newer {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// maybeDropTombstone applies the three-part tombstone removal rule to
// the oldest retained version of a key.
func maybeDropTombstone(kept []Record, snaps []uint64, lower Coverage) []Record {
	if len(kept) == 0 {
		return kept
	}
	last := kept[len(kept)-1]
	if !last.Del {
		return kept
	}
	if lower != nil && lower.MightContain(last.Key) {
		return kept // lower levels may still hold an older value
	}
	idx := sort.Search(len(snaps), func(j int) bool { return snaps[j] >= last.Seq })
	if idx < len(snaps) {
		return kept // a pinned snapshot still needs this tombstone
	}
	return kept[:len(kept)-1]
}
