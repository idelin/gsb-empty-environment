package lsm

import (
	"bytes"
	"fmt"
	"sort"
)

// Entry is one version of a user key.
type Entry struct {
	Key   []byte
	Seq   SeqNum
	Kind  ValueKind
	Value []byte
}

// InternalKey returns the MVCC identity of e.
func (e Entry) InternalKey() InternalKey {
	return InternalKey{UserKey: e.Key, Seq: e.Seq, Kind: e.Kind}
}

// UserKey returns the entry's user key.
func (e Entry) UserKey() []byte { return e.Key }

// Clone returns a deep copy of e.
func (e Entry) Clone() Entry {
	return Entry{
		Key:   cloneBytes(e.Key),
		Seq:   e.Seq,
		Kind:  e.Kind,
		Value: cloneBytes(e.Value),
	}
}

// RangeTombstone deletes every user key in [Start, End) whose sequence number
// is strictly smaller than Seq. A nil Start or End is an unbounded endpoint.
type RangeTombstone struct {
	Start []byte
	End   []byte
	Seq   SeqNum
}

// KeyRange returns the tombstone's half-open interval.
func (t RangeTombstone) KeyRange() KeyRange {
	return KeyRange{Start: t.Start, End: t.End}
}

// Clone returns a deep copy of t.
func (t RangeTombstone) Clone() RangeTombstone {
	return RangeTombstone{Start: cloneBytes(t.Start), End: cloneBytes(t.End), Seq: t.Seq}
}

// Clip restricts t to bounds. A tombstone that does not intersect bounds is
// rejected. Clipping preserves the tombstone's sequence number, which is what
// makes delivering a clipped copy to every intersecting subcompaction safe.
func (t RangeTombstone) Clip(bounds KeyRange) (RangeTombstone, bool) {
	if !t.KeyRange().Overlaps(bounds) {
		return RangeTombstone{}, false
	}
	out := t.Clone()
	if bounds.Start != nil && (out.Start == nil || bytes.Compare(out.Start, bounds.Start) < 0) {
		out.Start = cloneBytes(bounds.Start)
	}
	if bounds.End != nil && (out.End == nil || bytes.Compare(out.End, bounds.End) > 0) {
		out.End = cloneBytes(bounds.End)
	}
	if out.End != nil && out.Start != nil && bytes.Compare(out.Start, out.End) >= 0 {
		return RangeTombstone{}, false
	}
	return out, true
}

// Covers reports whether the tombstone covers key.
func (t RangeTombstone) Covers(key []byte) bool {
	return t.KeyRange().Contains(key)
}

// FileMeta describes one SSTable. Entries and RangeTombstones are kept in
// memory in this package; a real engine would load them from the table file.
type FileMeta struct {
	FileID          uint64
	Level           int
	Smallest        InternalKey
	Largest         InternalKey
	Size            uint64
	Entries         []Entry
	RangeTombstones []RangeTombstone
}

// SSTable and FileMetadata are compatibility aliases for FileMeta.
type SSTable = FileMeta

// FileMetadata is an alias for FileMeta.
type FileMetadata = FileMeta

// Table is an alias for FileMeta.
type Table = FileMeta

// Tombstone is an alias for RangeTombstone.
type Tombstone = RangeTombstone

// CompactionPlan is an alias for CompactionTask.
type CompactionPlan = CompactionTask

// Clone returns a deep copy of f.
func (f *FileMeta) Clone() *FileMeta {
	if f == nil {
		return nil
	}
	out := *f
	out.Smallest = f.Smallest.Clone()
	out.Largest = f.Largest.Clone()
	out.Entries = make([]Entry, len(f.Entries))
	for i := range f.Entries {
		out.Entries[i] = f.Entries[i].Clone()
	}
	out.RangeTombstones = make([]RangeTombstone, len(f.RangeTombstones))
	for i := range f.RangeTombstones {
		out.RangeTombstones[i] = f.RangeTombstones[i].Clone()
	}
	return &out
}

// Bounds returns the half-open hull of all point keys and range tombstones in
// the table. It is intentionally conservative: including a tombstone endpoint
// may pull in a neighbouring file, never omit a file whose keys are deleted.
func (f *FileMeta) Bounds() KeyRange {
	if f == nil {
		return KeyRange{}
	}
	var start, end []byte
	startSet := false
	startUnbounded := false
	endUnbounded := false
	lower := func(key []byte) {
		if startUnbounded {
			return
		}
		if !startSet || bytes.Compare(key, start) < 0 {
			start = cloneBytes(key)
			startSet = true
		}
	}
	upperInclusive := func(key []byte) {
		if endUnbounded {
			return
		}
		ub := UpperBound(key)
		if end == nil || bytes.Compare(ub, end) > 0 {
			end = ub
		}
	}
	upperExclusive := func(key []byte) {
		if endUnbounded {
			return
		}
		if end == nil || bytes.Compare(key, end) > 0 {
			end = cloneBytes(key)
		}
	}
	if f.Smallest.UserKey != nil || f.Largest.UserKey != nil || len(f.Entries) > 0 {
		lower(f.Smallest.UserKey)
		upperInclusive(f.Largest.UserKey)
	}
	for _, e := range f.Entries {
		lower(e.Key)
		upperInclusive(e.Key)
	}
	for _, t := range f.RangeTombstones {
		if t.Start == nil {
			startUnbounded = true
			start = nil
		} else {
			lower(t.Start)
		}
		if t.End == nil {
			endUnbounded = true
			end = nil
		} else {
			upperExclusive(t.End)
		}
	}
	if startUnbounded {
		start = nil
	}
	return KeyRange{Start: start, End: end}
}

// Range returns the inclusive user-key span of f. It is retained for callers
// that prefer inclusive file bounds.
func (f *FileMeta) Range() KeyRange {
	if f == nil {
		return KeyRange{}
	}
	return KeyRange{Start: cloneBytes(f.Smallest.UserKey), End: cloneBytes(f.Largest.UserKey)}
}

// MinKey returns the smallest user key in the table's hull.
func (f *FileMeta) MinKey() []byte {
	if f == nil {
		return nil
	}
	return cloneBytes(f.Bounds().Start)
}

// MaxKey returns the table's largest point key, or its exclusive hull endpoint
// for a tombstone-only table.
func (f *FileMeta) MaxKey() []byte {
	if f == nil {
		return nil
	}
	if f.Largest.UserKey != nil {
		return cloneBytes(f.Largest.UserKey)
	}
	b := f.Bounds()
	if b.End == nil {
		return nil
	}
	return cloneBytes(b.End)
}

// OverlapsRange reports whether any point or range key in f intersects r.
func (f *FileMeta) OverlapsRange(r KeyRange) bool {
	if f == nil || f.empty() {
		return false
	}
	if f.Bounds().Overlaps(r) {
		return true
	}
	for _, t := range f.RangeTombstones {
		if t.KeyRange().Overlaps(r) {
			return true
		}
	}
	return false
}

func (f *FileMeta) empty() bool {
	return f.Smallest.UserKey == nil && f.Largest.UserKey == nil &&
		len(f.Entries) == 0 && len(f.RangeTombstones) == 0
}

// OverlapsFile reports whether two tables' key extents intersect.
func (f *FileMeta) OverlapsFile(other *FileMeta) bool {
	if f == nil || other == nil {
		return false
	}
	return f.Bounds().Overlaps(other.Bounds())
}

// Overlaps is an alias for OverlapsFile.
func (f *FileMeta) Overlaps(other *FileMeta) bool { return f.OverlapsFile(other) }

// EffectiveSize returns Size, or an in-memory estimate when Size was not set.
func (f *FileMeta) EffectiveSize() uint64 {
	if f == nil {
		return 0
	}
	if f.Size != 0 {
		return f.Size
	}
	var n uint64
	for _, e := range f.Entries {
		n += uint64(len(e.Key) + len(e.Value) + 16)
	}
	for _, t := range f.RangeTombstones {
		n += uint64(len(t.Start) + len(t.End) + 16)
	}
	return n
}

// Normalize derives Smallest/Largest/Size from the in-memory entries when the
// caller did not provide metadata.
func (f *FileMeta) Normalize() {
	if f == nil {
		return
	}
	if len(f.Entries) > 0 {
		sort.SliceStable(f.Entries, func(i, j int) bool {
			return CompareInternalKeys(f.Entries[i].InternalKey(), f.Entries[j].InternalKey()) < 0
		})
		first := f.Entries[0]
		if f.Smallest.UserKey == nil {
			f.Smallest = first.InternalKey().Clone()
		}
		last := f.Entries[len(f.Entries)-1]
		if f.Largest.UserKey == nil {
			f.Largest = last.InternalKey().Clone()
		}
	}
	if f.Size == 0 {
		f.Size = f.EffectiveSize()
	}
}

// Validate checks the table's local invariants.
func (f *FileMeta) Validate() error {
	if f == nil {
		return fmt.Errorf("lsm: nil file metadata")
	}
	if f.Level < 0 {
		return fmt.Errorf("lsm: file %d has negative level %d", f.FileID, f.Level)
	}
	if f.Smallest.UserKey != nil && f.Largest.UserKey != nil &&
		bytes.Compare(f.Smallest.UserKey, f.Largest.UserKey) > 0 {
		return fmt.Errorf("lsm: file %d has inverted bounds", f.FileID)
	}
	for i := 1; i < len(f.Entries); i++ {
		if CompareInternalKeys(f.Entries[i-1].InternalKey(), f.Entries[i].InternalKey()) >= 0 {
			return fmt.Errorf("lsm: file %d entries are not strictly ordered", f.FileID)
		}
	}
	return nil
}

// NewFileMeta builds a file from entries and range tombstones and fills in its
// bounds and size.
func NewFileMeta(fileID uint64, level int, entries []Entry, tombstones []RangeTombstone) *FileMeta {
	f := &FileMeta{
		FileID:          fileID,
		Level:           level,
		Entries:         make([]Entry, len(entries)),
		RangeTombstones: make([]RangeTombstone, len(tombstones)),
	}
	for i := range entries {
		f.Entries[i] = entries[i].Clone()
	}
	for i := range tombstones {
		f.RangeTombstones[i] = tombstones[i].Clone()
	}
	f.Normalize()
	return f
}

// NewSSTable builds a metadata-only table with user-key bounds.
func NewSSTable(fileID uint64, level int, smallest, largest []byte, size uint64) *FileMeta {
	return &FileMeta{
		FileID:   fileID,
		Level:    level,
		Smallest: MakeInternalKey(smallest, 0, KindPut),
		Largest:  MakeInternalKey(largest, 0, KindPut),
		Size:     size,
	}
}

// NewTable is an alias for NewSSTable.
func NewTable(fileID uint64, level int, smallest, largest []byte, size uint64) *FileMeta {
	return NewSSTable(fileID, level, smallest, largest, size)
}
