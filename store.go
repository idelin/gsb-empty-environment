// Package factstore implements a bitemporal fact store.
//
// Every fact carries a business valid-time interval [ValidFrom, ValidTo)
// and a monotonically increasing transaction sequence number Seq assigned
// at insert time. Facts are never mutated in place: a correction is a new
// fact whose valid interval overlaps the past, and a deletion is a
// tombstone fact with its own valid interval.
//
// A query is a two-dimensional point lookup Get(key, validTime, asOf):
// among all facts for key whose valid interval covers validTime and whose
// Seq <= asOf, the one with the greatest Seq wins. Because old facts are
// retained, later corrections never leak into earlier knowledge snapshots.
package factstore

import (
	"errors"
	"sort"
	"sync"
)

// ErrInvalidRange is returned when ValidFrom >= ValidTo.
var ErrInvalidRange = errors.New("factstore: ValidFrom must be < ValidTo")

// Record is a single immutable bitemporal fact.
type Record struct {
	Key       string
	Value     interface{}
	ValidFrom int64  // inclusive business valid-from
	ValidTo   int64  // exclusive business valid-to
	Seq       uint64 // transaction sequence, assigned by the store
	Deleted   bool   // tombstone: retracts the key over the valid interval
}

// version is one entry in a segment's winner timeline: for any knowledge
// snapshot asOf >= TxFrom (and below the next entry's TxFrom), Rec is the
// winning record for the segment's valid interval.
type version struct {
	TxFrom uint64
	Rec    Record
}

// segment is a non-overlapping valid-time slice [From, To) together with
// the sequence of records that won it, ordered by ascending TxFrom.
type segment struct {
	From     int64
	To       int64
	Versions []version
}

// keyIndex holds the split segment timeline for one key.
type keyIndex struct {
	segments []segment // sorted by From, non-overlapping, gaps allowed
	records  []Record  // every record ever added for this key, in Seq order
}

// Store is an append-only bitemporal fact store. It is safe for
// concurrent use.
type Store struct {
	mu   sync.RWMutex
	seq  uint64
	log  []Record
	keys map[string]*keyIndex
}

// New returns an empty Store.
func New() *Store {
	return &Store{keys: make(map[string]*keyIndex)}
}

// Now returns the current transaction sequence, i.e. the asOf value that
// sees everything written so far.
func (s *Store) Now() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seq
}

// Put adds or corrects a fact for key over [validFrom, validTo).
func (s *Store) Put(key string, value interface{}, validFrom, validTo int64) (Record, error) {
	return s.add(key, value, validFrom, validTo, false)
}

// Delete retracts key over [validFrom, validTo) via a tombstone record.
func (s *Store) Delete(key string, validFrom, validTo int64) (Record, error) {
	return s.add(key, nil, validFrom, validTo, true)
}

func (s *Store) add(key string, value interface{}, validFrom, validTo int64, deleted bool) (Record, error) {
	if validFrom >= validTo {
		return Record{}, ErrInvalidRange
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	rec := Record{
		Key:       key,
		Value:     value,
		ValidFrom: validFrom,
		ValidTo:   validTo,
		Seq:       s.seq,
		Deleted:   deleted,
	}
	s.log = append(s.log, rec)
	idx := s.keys[key]
	if idx == nil {
		idx = &keyIndex{}
		s.keys[key] = idx
	}
	idx.add(rec)
	return rec, nil
}

// Get resolves the 2D point (validTime, asOf) for key: the visible record
// is the one with the greatest Seq among records with Seq <= asOf whose
// valid interval covers validTime. Tombstones resolve to found == false.
func (s *Store) Get(key string, validTime int64, asOf uint64) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx := s.keys[key]
	if idx == nil {
		return Record{}, false
	}
	seg := idx.segmentAt(validTime)
	if seg == nil {
		return Record{}, false
	}
	// Last version with TxFrom <= asOf wins; versions are Seq-sorted.
	i := sort.Search(len(seg.Versions), func(i int) bool {
		return seg.Versions[i].TxFrom > asOf
	}) - 1
	if i < 0 {
		return Record{}, false
	}
	rec := seg.Versions[i].Rec
	if rec.Deleted {
		return Record{}, false
	}
	return rec, true
}

// History returns every record ever written for key, in Seq order,
// including corrections and tombstones.
func (s *Store) History(key string) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx := s.keys[key]
	if idx == nil {
		return nil
	}
	out := make([]Record, len(idx.records))
	copy(out, idx.records)
	return out
}

// Log returns every record ever written, in Seq order.
func (s *Store) Log() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, len(s.log))
	copy(out, s.log)
	return out
}

// Segments exposes the split valid-time timeline for key. It is intended
// for inspection and tests: segments are sorted by From, non-overlapping,
// and each Versions list is sorted by ascending TxFrom.
func (s *Store) Segments(key string) []segment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	idx := s.keys[key]
	if idx == nil {
		return nil
	}
	out := make([]segment, len(idx.segments))
	for i, seg := range idx.segments {
		vs := make([]version, len(seg.Versions))
		copy(vs, seg.Versions)
		out[i] = segment{From: seg.From, To: seg.To, Versions: vs}
	}
	return out
}

// segmentAt finds the segment containing validTime, or nil.
func (idx *keyIndex) segmentAt(validTime int64) *segment {
	i := sort.Search(len(idx.segments), func(i int) bool {
		return idx.segments[i].To > validTime
	})
	if i < len(idx.segments) && idx.segments[i].From <= validTime {
		return &idx.segments[i]
	}
	return nil
}

// add folds rec into the key's segment timeline: existing segments
// overlapping [ValidFrom, ValidTo) are split at the record's boundaries,
// the record is appended as the newest winner of every covered slice, and
// gaps inside the interval become fresh single-version segments.
func (idx *keyIndex) add(rec Record) {
	idx.records = append(idx.records, rec)
	f, t := rec.ValidFrom, rec.ValidTo
	v := version{TxFrom: rec.Seq, Rec: rec}

	out := make([]segment, 0, len(idx.segments)+2)
	for _, seg := range idx.segments {
		if seg.To <= f || seg.From >= t {
			out = append(out, seg)
			continue
		}
		if seg.From < f {
			out = append(out, segment{From: seg.From, To: f, Versions: seg.Versions})
		}
		mid := segment{From: max64(seg.From, f), To: min64(seg.To, t)}
		mid.Versions = make([]version, len(seg.Versions)+1)
		copy(mid.Versions, seg.Versions)
		mid.Versions[len(seg.Versions)] = v
		out = append(out, mid)
		if seg.To > t {
			out = append(out, segment{From: t, To: seg.To, Versions: seg.Versions})
		}
	}

	// Fill gaps of [f, t) not covered by any existing segment.
	out = fillGaps(out, f, t, v)
	idx.segments = out
}

// fillGaps inserts fresh segments covering the parts of [f, t) not already
// covered by segs. segs must be sorted by From; the result stays sorted.
func fillGaps(segs []segment, f, t int64, v version) []segment {
	out := make([]segment, 0, len(segs)+2)
	cursor := f
	gapsDone := false
	emit := func(until int64) {
		if cursor < until {
			out = append(out, segment{From: cursor, To: until, Versions: []version{v}})
		}
	}
	for _, seg := range segs {
		if seg.To <= f {
			out = append(out, seg)
			continue
		}
		if seg.From >= t {
			if !gapsDone {
				emit(t)
				gapsDone = true
			}
			out = append(out, seg)
			continue
		}
		emit(seg.From)
		out = append(out, seg)
		if seg.To > cursor {
			cursor = seg.To
		}
	}
	if !gapsDone {
		emit(t)
	}
	return out
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
