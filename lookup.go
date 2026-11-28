package lsm

import "bytes"

// Lookup resolves a user key at snapshot. It returns found=false when no
// version or tombstone is visible, deleted=true when the newest visible event
// is a point or range tombstone, and otherwise the newest visible value.
func Lookup(files []*FileMeta, key []byte, snapshot SeqNum) (value []byte, found, deleted bool) {
	var point *Entry
	var rangeSeq SeqNum
	rangeSeen := false
	for _, f := range files {
		if f == nil {
			continue
		}
		for i := range f.Entries {
			e := &f.Entries[i]
			if !bytes.Equal(e.Key, key) || e.Seq > snapshot {
				continue
			}
			if point == nil || e.Seq > point.Seq ||
				(e.Seq == point.Seq && e.Kind > point.Kind) {
				copyEntry := e.Clone()
				point = &copyEntry
			}
		}
		for _, t := range f.RangeTombstones {
			if t.Seq > snapshot || !t.Covers(key) {
				continue
			}
			if !rangeSeen || t.Seq > rangeSeq {
				rangeSeq = t.Seq
				rangeSeen = true
			}
		}
	}
	if point == nil && !rangeSeen {
		return nil, false, false
	}
	if rangeSeen && (point == nil || rangeSeq > point.Seq) {
		return nil, true, true
	}
	switch point.Kind {
	case KindDelete, KindRangeDelete:
		return nil, true, true
	default:
		return cloneBytes(point.Value), true, false
	}
}

// LookupVersion resolves a key against every file in a version.
func LookupVersion(v *Version, key []byte, snapshot SeqNum) (value []byte, found, deleted bool) {
	if v == nil {
		return nil, false, false
	}
	var files []*FileMeta
	for _, level := range v.Levels {
		files = append(files, level...)
	}
	return Lookup(files, key, snapshot)
}
