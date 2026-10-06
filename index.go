// Package byteindex provides an append-only byte-string index with
// frozen snapshots and substring occurrence queries.
package byteindex

import "bytes"

// Index is an append-only byte string. The zero value is ready to use.
type Index struct {
	data []byte
}

// Append adds data to the end of the index. Previously frozen snapshots
// are unaffected: they never observe bytes appended after their freeze.
func (x *Index) Append(data []byte) {
	x.data = append(x.data, data...)
}

// Len returns the current length of the index in bytes.
func (x *Index) Len() int { return len(x.data) }

// Freeze returns an immutable snapshot of the index as it is now.
// Bytes appended later are invisible to this snapshot.
func (x *Index) Freeze() *Snapshot {
	// Copy so that later Appends can never overwrite snapshot contents
	// even if the backing array is reused, and so Release can drop the
	// only reference to the frozen bytes.
	buf := make([]byte, len(x.data))
	copy(buf, x.data)
	return &Snapshot{data: buf}
}

// Snapshot is a frozen view of an Index at the moment of Freeze.
type Snapshot struct {
	data     []byte
	released bool
}

// Len returns the length of the snapshot in bytes.
func (s *Snapshot) Len() int { return len(s.data) }

// Release drops the snapshot's reference to the frozen bytes, allowing
// them to be garbage collected. The snapshot must not be used afterwards;
// Find on a released snapshot panics.
func (s *Snapshot) Release() {
	s.data = nil
	s.released = true
}

// Find returns the start offsets of every occurrence of pattern in the
// snapshot, in increasing order. Overlapping occurrences are all reported.
// An empty pattern matches at every boundary, i.e. offsets 0..Len.
func (s *Snapshot) Find(pattern []byte) []int {
	if s.released {
		panic("byteindex: Find on released Snapshot")
	}
	var hits []int
	if len(pattern) == 0 {
		hits = make([]int, len(s.data)+1)
		for i := range hits {
			hits[i] = i
		}
		return hits
	}
	for start := 0; start+len(pattern) <= len(s.data); {
		i := bytes.Index(s.data[start:], pattern)
		if i < 0 {
			break
		}
		hits = append(hits, start+i)
		start += i + 1 // step by one to allow overlapping matches
	}
	return hits
}
