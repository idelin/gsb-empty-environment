// Package lsm implements a simplified LSM-tree compactor with MVCC
// snapshot semantics. Records carry a key, a sequence number and either
// a value or a tombstone. Segments are immutable sorted runs ordered by
// (key asc, seq desc).
package lsm

import "fmt"

// Record is a single versioned entry. Del marks a tombstone; Val is
// meaningless (and must be nil) when Del is set.
type Record struct {
	Key string
	Seq uint64
	Val []byte
	Del bool
}

func (r Record) String() string {
	if r.Del {
		return fmt.Sprintf("%s@%d:<del>", r.Key, r.Seq)
	}
	return fmt.Sprintf("%s@%d:%s", r.Key, r.Seq, r.Val)
}

// sameContent reports whether two records that share key and seq also
// carry identical content. A mismatch is corruption.
func sameContent(a, b Record) bool {
	if a.Del != b.Del {
		return false
	}
	if a.Del {
		return true
	}
	if len(a.Val) != len(b.Val) {
		return false
	}
	for i := range a.Val {
		if a.Val[i] != b.Val[i] {
			return false
		}
	}
	return true
}

// Segment is an immutable sorted run of records ordered by
// (Key ascending, Seq descending).
type Segment struct {
	ID      int
	Level   int
	Records []Record
}

// NewSegment validates ordering and returns a segment.
func NewSegment(id, level int, recs []Record) (*Segment, error) {
	for i := 1; i < len(recs); i++ {
		prev, cur := recs[i-1], recs[i]
		if prev.Key > cur.Key || (prev.Key == cur.Key && prev.Seq <= cur.Seq) {
			return nil, fmt.Errorf("lsm: segment %d records out of order at %d: %v then %v", id, i, prev, cur)
		}
	}
	return &Segment{ID: id, Level: level, Records: recs}, nil
}

// MustSegment is NewSegment that panics on error; handy for fixtures.
func MustSegment(id, level int, recs []Record) *Segment {
	s, err := NewSegment(id, level, recs)
	if err != nil {
		panic(err)
	}
	return s
}

func (s *Segment) MinKey() string { return s.Records[0].Key }
func (s *Segment) MaxKey() string { return s.Records[len(s.Records)-1].Key }

// RangeOverlaps reports whether [s.MinKey,s.MaxKey] intersects [lo,hi].
func (s *Segment) RangeOverlaps(lo, hi string) bool {
	return s.MinKey() <= hi && lo <= s.MaxKey()
}

// ContainsRange reports whether key falls inside the segment's key range.
// This is a range check only; the key may not actually be present.
func (s *Segment) ContainsRange(key string) bool {
	return s.MinKey() <= key && key <= s.MaxKey()
}

// HasKey reports whether the segment actually holds any version of key.
func (s *Segment) HasKey(key string) bool {
	if !s.ContainsRange(key) {
		return false
	}
	i := s.lowerBound(key)
	return i < len(s.Records) && s.Records[i].Key == key
}

// lowerBound returns the index of the first record with Key >= key.
func (s *Segment) lowerBound(key string) int {
	lo, hi := 0, len(s.Records)
	for lo < hi {
		mid := (lo + hi) / 2
		if s.Records[mid].Key < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
