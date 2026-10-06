// Package bitemp implements a bitemporal fact store.
//
// Each fact is a rectangle in a two-dimensional space:
//
//	valid time  (business-valid interval, half-open [From, To))
//	know time   (ingestion sequence number Seq, valid from Seq onward)
//
// A correction is simply a new rectangle with a higher Seq that overlaps
// the past. A query is a point (t, asOf): it sees exactly the rectangles
// with From <= t < To and Seq <= asOf, and the one with the greatest Seq
// wins. Because visibility is bounded by asOf, later corrections never
// leak into an older knowledge snapshot. Deletes are rectangles too
// (tombstones carrying their own valid interval).
package bitemp

import "sort"

// Record is a single ingestion event: an assertion (Del == false) that
// Key held Value over [From, To), or a retraction (Del == true) of the
// key over that interval.
type Record struct {
	Key   string
	Value string
	From  int64
	To    int64
	Seq   uint64
	Del   bool
}

// version is the projection of a Record onto one segment.
type version struct {
	seq   uint64
	value string
	del   bool
}

// segment is a maximal run of valid time on which the same ordered list
// of versions applies. Segments of one key are sorted and non-overlapping.
type segment struct {
	from, to int64
	vers     []version // ascending seq
}

type timeline struct {
	segs []segment
}

// Store is the interval-splitting implementation. Facts are appended,
// never mutated; corrections and deletes are new facts with higher Seq.
type Store struct {
	seq  uint64
	keys map[string]*timeline
}

func NewStore() *Store { return &Store{keys: make(map[string]*timeline)} }

// Put records that key held value over [from, to). Returns the assigned
// sequence number. Empty intervals are ignored and consume no sequence.
func (s *Store) Put(key, value string, from, to int64) uint64 {
	return s.add(Record{Key: key, Value: value, From: from, To: to})
}

// Delete records a tombstone for key over [from, to).
func (s *Store) Delete(key string, from, to int64) uint64 {
	return s.add(Record{Key: key, From: from, To: to, Del: true})
}

func (s *Store) add(r Record) uint64 {
	if r.From >= r.To {
		return s.seq
	}
	s.seq++
	r.Seq = s.seq
	tl := s.keys[r.Key]
	if tl == nil {
		tl = &timeline{}
		s.keys[r.Key] = tl
	}
	tl.apply(r)
	return s.seq
}

// Seq returns the current ingestion sequence number.
func (s *Store) Seq() uint64 { return s.seq }

// Get answers the 2D point query (t, asOf): among facts with
// From <= t < To and Seq <= asOf the greatest Seq wins; a winning
// tombstone means "no fact".
func (s *Store) Get(key string, t int64, asOf uint64) (string, bool) {
	tl := s.keys[key]
	if tl == nil {
		return "", false
	}
	i := sort.Search(len(tl.segs), func(i int) bool { return tl.segs[i].to > t })
	if i == len(tl.segs) || tl.segs[i].from > t {
		return "", false
	}
	vers := tl.segs[i].vers
	j := sort.Search(len(vers), func(k int) bool { return vers[k].seq > asOf }) - 1
	if j < 0 || vers[j].del {
		return "", false
	}
	return vers[j].value, true
}

// apply splits the timeline at the record's boundaries and appends the
// record's version to every segment inside [From, To).
func (tl *timeline) apply(r Record) {
	bounds := map[int64]bool{r.From: true, r.To: true}
	for _, sg := range tl.segs {
		bounds[sg.from] = true
		bounds[sg.to] = true
	}
	pts := make([]int64, 0, len(bounds))
	for p := range bounds {
		pts = append(pts, p)
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i] < pts[j] })

	next := make([]segment, 0, len(tl.segs)+1)
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		old := tl.segmentAt(a)
		inNew := r.From <= a && b <= r.To
		if old == nil && !inNew {
			continue
		}
		var vers []version
		if old != nil {
			vers = append(vers, old.vers...)
		}
		if inNew {
			vers = append(vers, version{seq: r.Seq, value: r.Value, del: r.Del})
		}
		next = append(next, segment{from: a, to: b, vers: vers})
	}
	tl.segs = next
}

func (tl *timeline) segmentAt(t int64) *segment {
	i := sort.Search(len(tl.segs), func(i int) bool { return tl.segs[i].to > t })
	if i == len(tl.segs) || tl.segs[i].from > t {
		return nil
	}
	return &tl.segs[i]
}
