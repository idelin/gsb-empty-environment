package lsm

import "sort"

// Stats counts records a reader actually examined.
type Stats struct{ RecordsRead int }

// Reader serves MVCC reads over a fixed set of segments, using key ranges
// and binary search to touch as few records as possible.
type Reader struct {
	Segments []*Segment
	Stats    *Stats
}

// Get returns the value visible at snapshot snap, or found=false if the
// visible version is a tombstone or no version exists.
func (r *Reader) Get(key string, snap uint64) (value []byte, found bool) {
	var best *Record
	for _, s := range r.Segments {
		if !s.CoversKey(key) {
			continue
		}
		i := sort.Search(len(s.Records), func(i int) bool {
			return s.Records[i].Key >= key
		})
		// Versions of the key are Seq-descending; the first one with
		// Seq <= snap is this segment's visible version, older ones
		// cannot win, so stop there.
		for i < len(s.Records) && s.Records[i].Key == key {
			rec := &s.Records[i]
			r.Stats.RecordsRead++
			if rec.Seq <= snap {
				if best == nil || rec.Seq > best.Seq {
					best = rec
				}
				break
			}
			i++
		}
	}
	if best == nil || best.Tombstone {
		return nil, false
	}
	return best.Value, true
}

// ReferenceReader resolves MVCC visibility by exhaustively scanning every
// record of every segment. It is the ground truth used to validate Reader
// and compaction outputs; its Stats prove the scan really happened.
type ReferenceReader struct {
	Segments []*Segment
	Stats    *Stats
}

// Get full-scans all segments and returns the newest version of key with
// Seq <= snap. Conflicting duplicates are reported as corruption.
func (r *ReferenceReader) Get(key string, snap uint64) (value []byte, found bool, err error) {
	var best *Record
	for _, s := range r.Segments {
		for i := range s.Records {
			rec := &s.Records[i]
			r.Stats.RecordsRead++
			if rec.Key != key || rec.Seq > snap {
				continue
			}
			if best != nil && best.Seq == rec.Seq {
				if !best.sameContent(*rec) {
					return nil, false, ErrCorruption
				}
				continue
			}
			if best == nil || rec.Seq > best.Seq {
				cp := *rec
				best = &cp
			}
		}
	}
	if best == nil || best.Tombstone {
		return nil, false, nil
	}
	return best.Value, true, nil
}
