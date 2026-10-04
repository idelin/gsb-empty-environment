// Package lsm implements a simplified LSM-tree compactor with MVCC
// semantics: records carry a sequence number, readers pin snapshots,
// and tombstones may only be dropped when it is provably safe.
package lsm

import (
	"bytes"
	"sort"
)

// Record is a single MVCC version of a key. A record with Tombstone set
// deletes all older versions of the key.
type Record struct {
	Key       string
	Seq       uint64
	Value     []byte
	Tombstone bool
}

func (r Record) sameContent(o Record) bool {
	return r.Tombstone == o.Tombstone && bytes.Equal(r.Value, o.Value)
}

// Segment is an immutable sorted run of records ordered by
// (Key ascending, Seq descending).
type Segment struct {
	ID      uint64
	Level   int
	Records []Record
}

// NewSegment copies and sorts recs into the canonical order.
func NewSegment(id uint64, level int, recs []Record) *Segment {
	cp := make([]Record, len(recs))
	copy(cp, recs)
	sortRecords(cp)
	return &Segment{ID: id, Level: level, Records: cp}
}

func sortRecords(recs []Record) {
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Key != recs[j].Key {
			return recs[i].Key < recs[j].Key
		}
		return recs[i].Seq > recs[j].Seq
	})
}

// Range returns the inclusive key range of the segment.
func (s *Segment) Range() (lo, hi string, ok bool) {
	if len(s.Records) == 0 {
		return "", "", false
	}
	return s.Records[0].Key, s.Records[len(s.Records)-1].Key, true
}

// CoversKey reports whether key falls inside the segment's key range.
// Note this is a range check only: the key may not actually be present.
func (s *Segment) CoversKey(key string) bool {
	lo, hi, ok := s.Range()
	return ok && lo <= key && key <= hi
}
