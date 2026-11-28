package lsm

import (
	"bytes"
	"fmt"
)

// SeqNum is the monotonically increasing sequence number assigned to every
// write. Larger values are newer. It is an alias so engines can pass their own
// uint64 sequence counters without conversions.
type SeqNum = uint64

// MaxSeqNum is newer than every sequence number produced by an engine.
const MaxSeqNum SeqNum = ^SeqNum(0)

// ValueKind describes the payload carried by an internal key.
type ValueKind uint8

const (
	// KindPut stores a value.
	KindPut ValueKind = iota
	// KindDelete is a point delete tombstone.
	KindDelete
	// KindRangeDelete marks a half-open range as deleted.
	KindRangeDelete
)

// RocksDB-style aliases. They are useful when adapting code written against
// RocksDB's terminology.
const (
	TypeValue         = KindPut
	TypeDeletion      = KindDelete
	TypeRangeDeletion = KindRangeDelete
)

func (k ValueKind) String() string {
	switch k {
	case KindPut:
		return "put"
	case KindDelete:
		return "delete"
	case KindRangeDelete:
		return "range-delete"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

// InternalKey is a user key annotated with the sequence number and operation
// kind used for MVCC ordering.
type InternalKey struct {
	UserKey []byte
	Seq     SeqNum
	Kind    ValueKind
}

// MakeInternalKey constructs an internal key. The slices are copied so that
// callers can reuse their buffers.
func MakeInternalKey(userKey []byte, seq SeqNum, kind ValueKind) InternalKey {
	return InternalKey{UserKey: cloneBytes(userKey), Seq: seq, Kind: kind}
}

// Clone returns a deep copy of k.
func (k InternalKey) Clone() InternalKey {
	k.UserKey = cloneBytes(k.UserKey)
	return k
}

// CompareInternalKeys orders keys by user key ascending, sequence number
// descending, and kind descending. Therefore the first entry for a user key is
// its newest version, and a deletion wins a same-sequence tie with a put.
func CompareInternalKeys(a, b InternalKey) int {
	if c := bytes.Compare(a.UserKey, b.UserKey); c != 0 {
		return c
	}
	if a.Seq > b.Seq {
		return -1
	}
	if a.Seq < b.Seq {
		return 1
	}
	if a.Kind > b.Kind {
		return -1
	}
	if a.Kind < b.Kind {
		return 1
	}
	return 0
}

// CompareInternalKey is an alias kept for callers that prefer the singular
// form.
func CompareInternalKey(a, b InternalKey) int { return CompareInternalKeys(a, b) }

// KeyRange is a half-open user-key interval [Start, End). A nil Start means
// negative infinity and a nil End means positive infinity. An empty but
// non-nil slice is a real key.
type KeyRange struct {
	Start []byte
	End   []byte
}

// MakeKeyRange copies and returns a half-open range.
func MakeKeyRange(start, end []byte) KeyRange {
	return KeyRange{Start: cloneBytes(start), End: cloneBytes(end)}
}

// Clone returns a deep copy of r.
func (r KeyRange) Clone() KeyRange {
	return KeyRange{Start: cloneBytes(r.Start), End: cloneBytes(r.End)}
}

// Empty reports whether the range cannot contain any key.
func (r KeyRange) Empty() bool {
	return r.Start != nil && r.End != nil && bytes.Compare(r.Start, r.End) >= 0
}

// Contains reports whether key belongs to [Start, End).
func (r KeyRange) Contains(key []byte) bool {
	if r.Start != nil && bytes.Compare(key, r.Start) < 0 {
		return false
	}
	if r.End != nil && bytes.Compare(key, r.End) >= 0 {
		return false
	}
	return true
}

// Overlaps reports whether two half-open intervals have a non-empty
// intersection. Unbounded endpoints are supported.
func (r KeyRange) Overlaps(other KeyRange) bool {
	if r.Empty() || other.Empty() {
		return false
	}
	if r.End != nil && other.Start != nil && bytes.Compare(r.End, other.Start) <= 0 {
		return false
	}
	if other.End != nil && r.Start != nil && bytes.Compare(other.End, r.Start) <= 0 {
		return false
	}
	return true
}

// Intersect returns the intersection of r and other. ok is false when the
// intersection is empty.
func (r KeyRange) Intersect(other KeyRange) (KeyRange, bool) {
	if !r.Overlaps(other) {
		return KeyRange{}, false
	}
	out := KeyRange{Start: r.Start, End: r.End}
	if other.Start != nil && (out.Start == nil || bytes.Compare(other.Start, out.Start) > 0) {
		out.Start = other.Start
	}
	if other.End != nil && (out.End == nil || bytes.Compare(other.End, out.End) < 0) {
		out.End = other.End
	}
	return out.Clone(), true
}

// Before reports whether r is entirely before other. Adjacent ranges are
// ordered: [a,b) is before [b,c).
func (r KeyRange) Before(other KeyRange) bool {
	if r.Empty() || other.Empty() {
		return false
	}
	return r.End != nil && other.Start != nil && bytes.Compare(r.End, other.Start) <= 0
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// UpperBound returns an exclusive key that sorts after every key having b as a
// prefix. It is a convenient finite stand-in for the positive infinity
// endpoint of a file whose last key is b.
func UpperBound(b []byte) []byte {
	out := make([]byte, len(b)+1)
	copy(out, b)
	out[len(b)] = 0
	return out
}

// CompareUserKeys compares two user keys in bytewise order.
func CompareUserKeys(a, b []byte) int { return bytes.Compare(a, b) }
