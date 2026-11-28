// Package compaction implements a RocksDB-style leveled LSM-tree background
// compaction subsystem.
//
// It bundles three cooperating components:
//
//   - A leveled compaction scheduler that scores levels by amplification,
//     expands compaction inputs according to LSM overlap rules and never
//     starts a job whose files are locked by another active job.
//   - A concurrent subcompaction engine that splits a compaction into
//     disjoint half-open key spans, clips range tombstones across every span
//     they intersect and merges them in parallel.
//   - A WriteStall controller that drives the Normal/Delayed/Stopped write
//     admission state machine from L0 file count and pending compaction
//     bytes.
package compaction

import "errors"

// UserKey is the externally visible key. Ordering is lexicographic byte order.
type UserKey string

// EntryKind distinguishes value writes from deletion markers.
type EntryKind uint8

const (
	KindPut EntryKind = iota
	KindDelete
)

// InternalKey pairs a user key with the sequence number of the write. Keys
// with the same user key are ordered newest-first (descending sequence).
type InternalKey struct {
	UserKey UserKey
	Seq     uint64
	Kind    EntryKind
}

// Entry is one version of a user key. Value is nil for KindDelete entries.
type Entry struct {
	InternalKey
	Value []byte
}

// RangeTombstone is a range deletion [Start, End) anchored at sequence Seq.
// It hides every point entry with a sequence <= Seq whose user key falls in
// the half-open interval.
type RangeTombstone struct {
	Start UserKey // inclusive
	End   UserKey // exclusive
	Seq   uint64
}

// Covers reports whether k falls inside the tombstone's key interval.
func (t RangeTombstone) Covers(k UserKey) bool {
	return k >= t.Start && k < t.End
}

// Empty reports whether the clipped interval contains no key.
func (t RangeTombstone) Empty() bool {
	return t.Start >= t.End
}

// KeyRange is the inclusive user-key footprint of an SSTable.
type KeyRange struct {
	Smallest UserKey
	Largest  UserKey
}

// Overlaps reports whether two inclusive key ranges intersect.
func (r KeyRange) Overlaps(o KeyRange) bool {
	return r.Smallest <= o.Largest && o.Smallest <= r.Largest
}

// SSTable is an immutable sorted string table. Once published in a Version
// its contents never change; compaction replaces whole files atomically.
type SSTable struct {
	FileNumber   uint64
	Level        int
	Smallest     UserKey
	Largest      UserKey
	Entries      []Entry
	RangeDeletes []RangeTombstone
	SizeBytes    int64
}

// Range returns the inclusive key footprint of the file.
func (s *SSTable) Range() KeyRange {
	return KeyRange{Smallest: s.Smallest, Largest: s.Largest}
}

// Overlaps reports whether the file footprint intersects r.
func (s *SSTable) Overlaps(r KeyRange) bool {
	return s.Range().Overlaps(r)
}

// Version is an immutable snapshot of the level layout. Levels are indexed
// from zero; level zero permits overlapping files while every level L >= 1
// holds strictly ordered, mutually non-overlapping files.
type Version struct {
	Levels [][]*SSTable
}

// Files returns the files of a level.
func (v *Version) Files(level int) []*SSTable {
	if level < 0 || level >= len(v.Levels) {
		return nil
	}
	return v.Levels[level]
}

// ErrNoCompaction is returned by scheduling helpers when no level needs work.
var ErrNoCompaction = errors.New("compaction: no eligible compaction candidate")

func compareInternal(a, b InternalKey) int {
	switch {
	case a.UserKey < b.UserKey:
		return -1
	case a.UserKey > b.UserKey:
		return 1
	}
	switch {
	case a.Seq > b.Seq: // newer sequence sorts first
		return -1
	case a.Seq < b.Seq:
		return 1
	}
	switch {
	case a.Kind < b.Kind:
		return -1
	case a.Kind > b.Kind:
		return 1
	}
	return 0
}

func entrySize(e Entry) int64 {
	return int64(len(e.UserKey)) + int64(len(e.Value)) + 16
}

func tombstoneSize(t RangeTombstone) int64 {
	return int64(len(t.Start)) + int64(len(t.End)) + 16
}

func fileRange(files []*SSTable) KeyRange {
	if len(files) == 0 {
		return KeyRange{}
	}
	r := files[0].Range()
	for _, f := range files[1:] {
		if f.Smallest < r.Smallest {
			r.Smallest = f.Smallest
		}
		if f.Largest > r.Largest {
			r.Largest = f.Largest
		}
	}
	return r
}

func fileNumberSet(files []*SSTable) map[uint64]struct{} {
	out := make(map[uint64]struct{}, len(files))
	for _, f := range files {
		out[f.FileNumber] = struct{}{}
	}
	return out
}
