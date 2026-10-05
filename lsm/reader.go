package lsm

import "fmt"

// Stats counts records actually examined by a read. Tests assert on
// these counters instead of wall-clock timings.
type Stats struct {
	RecordsRead int
}

// Reader serves point reads against a specific (possibly old) version.
type Reader struct {
	v *Version
}

func NewReader(v *Version) *Reader { return &Reader{v: v} }

// Get returns the value of key visible at snapshot seq snapSeq, i.e. the
// record with the highest Seq <= snapSeq. found is false when that
// record is a tombstone or no version qualifies. Segments are consulted
// newest-level first; within a segment a binary search plus a short scan
// keeps RecordsRead proportional to matching versions, not segment size.
func (r *Reader) Get(snapSeq uint64, key string, st *Stats) (val []byte, found bool, err error) {
	var best *Record
	for _, lvl := range r.v.Levels {
		for _, s := range lvl {
			if !s.ContainsRange(key) {
				continue
			}
			i := s.lowerBound(key)
			for i < len(s.Records) && s.Records[i].Key == key {
				rec := s.Records[i]
				st.RecordsRead++
				i++
				if rec.Seq > snapSeq {
					continue
				}
				if best == nil || rec.Seq > best.Seq {
					cp := rec
					best = &cp
				} else if rec.Seq == best.Seq && !sameContent(*best, rec) {
					return nil, false, fmt.Errorf("%w: key %q seq %d", ErrCorruption, key, rec.Seq)
				}
				break // versions are seq-desc: first visible is newest here
			}
		}
	}
	if best == nil || best.Del {
		return nil, false, nil
	}
	return best.Val, true, nil
}

// ReferenceGet is the MVCC ground truth: it scans every record of every
// segment and picks the visible version. It also reports how many
// records it touched, which an indexed Get must never exceed.
func ReferenceGet(segs []*Segment, snapSeq uint64, key string) (val []byte, found bool, read int, err error) {
	var best *Record
	for _, s := range segs {
		for _, rec := range s.Records {
			read++
			if rec.Key != key || rec.Seq > snapSeq {
				continue
			}
			if best == nil || rec.Seq > best.Seq {
				cp := rec
				best = &cp
			} else if rec.Seq == best.Seq && !sameContent(*best, rec) {
				return nil, false, read, fmt.Errorf("%w: key %q seq %d", ErrCorruption, key, rec.Seq)
			}
		}
	}
	if best == nil || best.Del {
		return nil, false, read, nil
	}
	return best.Val, true, read, nil
}
