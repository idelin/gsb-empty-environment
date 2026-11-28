package lsm

import (
	"bytes"
	"sort"
)

// MergeOptions controls version elision during a compaction merge.
type MergeOptions struct {
	// Snapshots lists active snapshot sequence numbers. A snapshot anchors
	// every version needed to answer a read at that sequence.
	Snapshots []SeqNum
	// Bottommost must be true only when the output level has no older
	// overlapping keys.
	Bottommost bool
}

// CanDropObsolete reports whether this merge may physically discard old
// versions and range tombstones. Both conditions from the LSM safety rule are
// required: a bottommost output and no active snapshot anchor.
func CanDropObsolete(opts MergeOptions) bool {
	return opts.Bottommost && len(opts.Snapshots) == 0
}

// MergeResult is the materialised content of a compaction merge.
type MergeResult struct {
	Entries           []Entry
	RangeTombstones   []RangeTombstone
	DroppedEntries    int
	DroppedTombstones int
}

// MergeCompaction merges point versions and range tombstones. It always
// preserves enough history for active snapshots. When the output is bottommost
// and no snapshot is active, it collapses each user key to its newest visible
// version and physically discards range tombstones.
func MergeCompaction(entries []Entry, tombstones []RangeTombstone, opts MergeOptions) MergeResult {
	points := uniqueEntries(entries)
	sort.SliceStable(points, func(i, j int) bool {
		return CompareInternalKeys(points[i].InternalKey(), points[j].InternalKey()) < 0
	})
	tombs := uniqueTombstones(tombstones)
	sort.SliceStable(tombs, func(i, j int) bool {
		if c := bytes.Compare(tombs[i].Start, tombs[j].Start); c != 0 {
			return c < 0
		}
		if c := bytes.Compare(tombs[i].End, tombs[j].End); c != 0 {
			return c < 0
		}
		return tombs[i].Seq > tombs[j].Seq
	})

	result := MergeResult{}
	if !CanDropObsolete(opts) {
		// Conservative path: keep every point version and every tombstone.
		// This cannot resurrect a key for any snapshot and honours the rule
		// that physical elision is reserved for bottommost, snapshot-free
		// compactions.
		result.Entries = points
		result.RangeTombstones = tombs
		return result
	}

	// Bottommost, no snapshots: only the newest event per user key matters.
	for i := 0; i < len(points); {
		j := i + 1
		for j < len(points) && bytes.Equal(points[j].Key, points[i].Key) {
			j++
		}
		newest := points[i] // points are seq-desc within a user key
		deletedByRange := false
		var newestTombSeq SeqNum
		for _, t := range tombs {
			if t.Covers(newest.Key) && t.Seq > newestTombSeq {
				newestTombSeq = t.Seq
			}
		}
		if newestTombSeq > newest.Seq {
			deletedByRange = true
		}
		if deletedByRange || newest.Kind == KindDelete || newest.Kind == KindRangeDelete {
			result.DroppedEntries += j - i
		} else {
			result.Entries = append(result.Entries, newest)
			result.DroppedEntries += j - i - 1
		}
		i = j
	}
	result.DroppedTombstones = len(tombs)
	return result
}

// MergeEntries is a convenience wrapper that only returns point entries.
func MergeEntries(entries []Entry, tombstones []RangeTombstone, opts MergeOptions) []Entry {
	return MergeCompaction(entries, tombstones, opts).Entries
}

func uniqueEntries(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	type identity struct {
		key    string
		keyNil bool
		seq    SeqNum
		kind   ValueKind
	}
	seen := make(map[identity]bool, len(entries))
	for _, e := range entries {
		id := identity{key: string(e.Key), keyNil: e.Key == nil, seq: e.Seq, kind: e.Kind}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, e.Clone())
	}
	return out
}

func uniqueTombstones(tombs []RangeTombstone) []RangeTombstone {
	out := make([]RangeTombstone, 0, len(tombs))
	type identity struct {
		start    string
		startNil bool
		end      string
		endNil   bool
		seq      SeqNum
	}
	seen := make(map[identity]bool, len(tombs))
	for _, t := range tombs {
		id := identity{
			start: string(t.Start), startNil: t.Start == nil,
			end: string(t.End), endNil: t.End == nil,
			seq: t.Seq,
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, t.Clone())
	}
	return out
}
