package blkdict

import (
	"fmt"
	"sort"
)

// Segment is an immutable sorted string dictionary: concatenated blocks plus
// a block offset table. Once built it is never modified; batch updates merge
// old segments into a brand new one.
type Segment struct {
	id       uint64
	data     []byte   // concatenated encoded blocks
	blockOff []int    // offset of each block inside data
	count    int      // total number of keys
	refs     int      // pins: active view + open snapshots
	fkeys    []string // lazily validated restart-key index
}

// buildSegment builds an immutable segment from sorted keys.
// Duplicate policy: duplicates are REJECTED with an error (the dictionary is
// a set of unique keys). Empty strings are allowed, including as first key.
// Keys are compared byte-wise, which is also UTF-8 code-point order.
func buildSegment(id uint64, keys []string) (*Segment, error) {
	for i := 1; i < len(keys); i++ {
		if keys[i-1] == keys[i] {
			return nil, fmt.Errorf("blkdict: duplicate key %q", keys[i])
		}
		if keys[i-1] > keys[i] {
			return nil, fmt.Errorf("blkdict: keys not sorted at index %d", i)
		}
	}
	seg := &Segment{id: id, count: len(keys), refs: 1}
	for start := 0; start < len(keys); start += BlockSize {
		end := start + BlockSize
		if end > len(keys) {
			end = len(keys)
		}
		seg.blockOff = append(seg.blockOff, len(seg.data))
		seg.data = append(seg.data, EncodeBlock(keys[start:end])...)
	}
	return seg, nil
}

// Len returns the number of keys in the segment.
func (s *Segment) Len() int { return s.count }

// Bytes returns the exact number of encoded bytes (blocks only, excluding
// the in-memory offset table).
func (s *Segment) Bytes() int { return len(s.data) }

// Blocks returns the number of blocks.
func (s *Segment) Blocks() int { return len(s.blockOff) }

func (s *Segment) blockSlice(b int) []byte {
	end := len(s.data)
	if b+1 < len(s.blockOff) {
		end = s.blockOff[b+1]
	}
	return s.data[s.blockOff[b]:end]
}

// Get returns the k-th key (0-based). It locates the block arithmetically
// and decodes at most k%BlockSize+1 entries; the number of entries decoded
// is returned for statistics. Corruption is reported, never misdecoded.
func (s *Segment) Get(k int) (string, int, error) {
	if k < 0 || k >= s.count {
		return "", 0, fmt.Errorf("blkdict: index %d out of range [0,%d)", k, s.count)
	}
	b := k / BlockSize
	keys, err := decodeBlock(s.blockSlice(b), k%BlockSize+1)
	if err != nil {
		return "", 0, err
	}
	return keys[len(keys)-1], len(keys), nil
}

// firstKeys returns the validated restart-key index, built lazily.
func (s *Segment) firstKeys() ([]string, error) {
	if s.fkeys != nil {
		return s.fkeys, nil
	}
	fks := make([]string, len(s.blockOff))
	for b := range s.blockOff {
		fk, err := blockFirstKey(s.blockSlice(b))
		if err != nil {
			return nil, err
		}
		fks[b] = fk
	}
	s.fkeys = fks
	return fks, nil
}

// PrefixRange returns all keys carrying the given BYTE prefix, in order.
// The prefix may split a UTF-8 sequence; returned keys are always complete
// stored strings. An empty prefix returns the whole segment.
func (s *Segment) PrefixRange(prefix string) ([]string, error) {
	lo := prefix
	hi, hasHi := prefixSuccessor(prefix)
	fks, err := s.firstKeys()
	if err != nil {
		return nil, err
	}
	// First block whose restart key >= lo; matches may begin one block earlier.
	b := sort.Search(len(fks), func(i int) bool { return fks[i] >= lo })
	if b > 0 {
		b--
	}
	var out []string
	for ; b < len(s.blockOff); b++ {
		keys, err := decodeBlock(s.blockSlice(b), BlockSize)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if k < lo {
				continue
			}
			if hasHi && k >= hi {
				return out, nil
			}
			out = append(out, k)
		}
	}
	return out, nil
}

// allKeys fully decodes the segment (used by merge).
func (s *Segment) allKeys() ([]string, error) {
	out := make([]string, 0, s.count)
	for b := range s.blockOff {
		keys, err := decodeBlock(s.blockSlice(b), BlockSize)
		if err != nil {
			return nil, err
		}
		out = append(out, keys...)
	}
	return out, nil
}

// mergeKeys unions two sorted unique slices; keys present in both are kept
// once (idempotent re-insert).
func mergeKeys(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return append(append(out, a[i:]...), b[j:]...)
}
