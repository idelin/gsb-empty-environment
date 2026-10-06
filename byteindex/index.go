// Package byteindex provides an append-only byte-string index with frozen
// snapshots and substring occurrence queries.
//
// An Index accepts appended data. A Snapshot freezes the index at a point in
// time: queries on a snapshot only see data appended before the snapshot was
// taken, never later suffixes. Query results are start positions in
// ascending order and overlapping occurrences are reported.
package byteindex

// Index is an append-only byte-string index.
type Index struct {
	buf []byte
}

// New returns an empty Index.
func New() *Index {
	return &Index{}
}

// Append adds data to the end of the index. Snapshots taken before this call
// are unaffected.
func (ix *Index) Append(data []byte) {
	ix.buf = append(ix.buf, data...)
}

// Len returns the total number of bytes appended so far.
func (ix *Index) Len() int {
	return len(ix.buf)
}

// Snapshot freezes the index at its current length. The returned Snapshot
// observes exactly the bytes appended so far, even if the Index keeps
// growing afterwards.
func (ix *Index) Snapshot() *Snapshot {
	return &Snapshot{
		data: ix.buf,
		n:    len(ix.buf),
		live: true,
	}
}

// Snapshot is a frozen, read-only view of an Index. It must be Released when
// no longer needed; querying a released snapshot returns nil.
type Snapshot struct {
	data []byte // shared backing array of the Index
	n    int    // frozen length: only data[:n] is visible
	live bool
}

// Len returns the number of bytes visible to this snapshot.
func (s *Snapshot) Len() int {
	if !s.live {
		return 0
	}
	return s.n
}

// Release invalidates the snapshot. It is idempotent; after Release, FindAll
// returns nil and Len returns 0.
func (s *Snapshot) Release() {
	s.data = nil
	s.live = false
	s.n = 0
}

// FindAll returns the start positions of every occurrence of pattern in the
// frozen view, in ascending order. Overlapping occurrences are all reported.
// An empty pattern matches at every boundary 0..Len inclusive. A released
// snapshot returns nil.
func (s *Snapshot) FindAll(pattern []byte) []int {
	if !s.live {
		return nil
	}
	text := s.data[:s.n]
	if len(pattern) == 0 {
		pos := make([]int, s.n+1)
		for i := range pos {
			pos[i] = i
		}
		return pos
	}
	if len(pattern) > s.n {
		return nil
	}
	return kmpSearch(text, pattern)
}

// kmpSearch finds all (possibly overlapping) occurrences of pattern in text
// using the Knuth-Morris-Pratt algorithm.
func kmpSearch(text, pattern []byte) []int {
	lps := make([]int, len(pattern))
	for i, length := 1, 0; i < len(pattern); {
		if pattern[i] == pattern[length] {
			length++
			lps[i] = length
			i++
		} else if length > 0 {
			length = lps[length-1]
		} else {
			lps[i] = 0
			i++
		}
	}
	var pos []int
	for i, j := 0, 0; i < len(text); {
		if text[i] == pattern[j] {
			i++
			j++
			if j == len(pattern) {
				pos = append(pos, i-j)
				j = lps[j-1]
			}
		} else if j > 0 {
			j = lps[j-1]
		} else {
			i++
		}
	}
	return pos
}
