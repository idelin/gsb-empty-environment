package invindex

import "encoding/binary"

// PostingList is the decoded in-memory view of one term's compressed posting
// list: delta-varint encoded (docID, freq) pairs plus a skip table.
type PostingList struct {
	n        int         // number of postings
	interval int         // distance between skip pointers
	skips    []SkipEntry // sorted by DocID
	data     []byte      // varint(delta docID) varint(freq) ...
}

// SkipEntry lets an iterator jump directly to the posting at Index without
// decoding the postings in between.
type SkipEntry struct {
	Index  int    // posting index this entry points at
	DocID  uint64 // absolute docID of that posting
	Offset int    // byte offset of that posting inside data
}

// Len returns the number of postings in the list.
func (pl *PostingList) Len() int { return pl.n }

// SkipCount returns the number of skip pointers in the list.
func (pl *PostingList) SkipCount() int { return len(pl.skips) }

// encodePostingList appends the encoded posting list for docs (ascending)
// to dst and returns the extended slice.
//
// Format:
//
//	uvarint n, uvarint interval, uvarint numSkips,
//	numSkips * (uvarint indexDelta, uvarint docDelta, uvarint offset),
//	uvarint dataLen, data
func encodePostingList(dst []byte, docs []uint64, freqs map[uint64]uint32) []byte {
	n := len(docs)
	interval := skipInterval(n)

	type skip struct {
		index  int
		doc    uint64
		offset int
	}
	var skips []skip
	data := make([]byte, 0, n*2)
	prev := uint64(0)
	for i, d := range docs {
		if i > 0 && i%interval == 0 {
			skips = append(skips, skip{index: i, doc: d, offset: len(data)})
		}
		data = appendUvarint(data, d-prev)
		data = appendUvarint(data, uint64(freqs[d]))
		prev = d
	}

	dst = appendUvarint(dst, uint64(n))
	dst = appendUvarint(dst, uint64(interval))
	dst = appendUvarint(dst, uint64(len(skips)))
	prevDoc, prevIdx := uint64(0), 0
	for _, s := range skips {
		dst = appendUvarint(dst, uint64(s.index-prevIdx))
		dst = appendUvarint(dst, s.doc-prevDoc)
		dst = appendUvarint(dst, uint64(s.offset))
		prevDoc, prevIdx = s.doc, s.index
	}
	dst = appendUvarint(dst, uint64(len(data)))
	return append(dst, data...)
}

// decodePostingList parses the header and skip table of an encoded posting
// list. The posting data itself is decoded lazily by iterators.
func decodePostingList(raw []byte) *PostingList {
	pos := 0
	read := func() uint64 {
		v, n := binary.Uvarint(raw[pos:])
		pos += n
		return v
	}
	pl := &PostingList{}
	pl.n = int(read())
	pl.interval = int(read())
	numSkips := int(read())
	prevDoc, prevIdx := uint64(0), 0
	for i := 0; i < numSkips; i++ {
		idx := prevIdx + int(read())
		doc := prevDoc + read()
		off := int(read())
		pl.skips = append(pl.skips, SkipEntry{Index: idx, DocID: doc, Offset: off})
		prevDoc, prevIdx = doc, idx
	}
	dataLen := int(read())
	pl.data = raw[pos : pos+dataLen]
	return pl
}

// Iterator walks a PostingList sequentially or via skip pointers.
type Iterator struct {
	pl      *PostingList
	idx     int    // index of current posting; -1 = before first, n = exhausted
	off     int    // byte offset of current posting
	nextOff int    // byte offset of the posting after the current one
	doc     uint64 // docID of current posting
	freq    uint32 // frequency of current posting

	// Statistics, useful to observe skip-pointer effectiveness.
	Decoded int // number of postings actually decoded
	Skips   int // number of skip-pointer jumps taken
}

// Iterator returns an iterator positioned before the first posting.
func (pl *PostingList) Iterator() *Iterator {
	return &Iterator{pl: pl, idx: -1}
}

// Doc returns the docID of the current posting.
func (it *Iterator) Doc() uint64 { return it.doc }

// Freq returns the term frequency of the current posting.
func (it *Iterator) Freq() uint32 { return it.freq }

// decodeEntry reads one (delta, freq) pair at off, returning the pair and the
// encoded size in bytes.
func decodeEntry(data []byte, off int) (delta, freq uint64, size int) {
	d, n1 := binary.Uvarint(data[off:])
	f, n2 := binary.Uvarint(data[off+n1:])
	return d, f, n1 + n2
}

// Next advances to the next posting. It returns false when the list is
// exhausted.
func (it *Iterator) Next() bool {
	if it.idx+1 >= it.pl.n {
		it.idx = it.pl.n
		return false
	}
	if it.idx == -1 {
		it.off = 0
		it.doc = 0
	} else {
		it.off = it.nextOff
	}
	delta, freq, size := decodeEntry(it.pl.data, it.off)
	it.doc += delta
	it.freq = uint32(freq)
	it.nextOff = it.off + size
	it.idx++
	it.Decoded++
	return true
}

// Advance moves the iterator to the first posting with docID >= target,
// using skip pointers to leap over regions that cannot contain target.
// It returns false if no such posting exists.
func (it *Iterator) Advance(target uint64) bool {
	if it.idx == -1 {
		if !it.Next() {
			return false
		}
	}
	if it.idx >= it.pl.n {
		return false
	}
	if it.doc >= target {
		return true
	}
	// Find the last skip entry whose docID is still <= target; jumping
	// there can never skip over a posting >= target.
	skips := it.pl.skips
	lo, hi := 0, len(skips)
	for lo < hi {
		mid := (lo + hi) / 2
		if skips[mid].DocID <= target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo > 0 && skips[lo-1].Index > it.idx {
		s := skips[lo-1]
		_, freq, size := decodeEntry(it.pl.data, s.Offset)
		it.idx = s.Index
		it.off = s.Offset
		it.nextOff = s.Offset + size
		it.doc = s.DocID
		it.freq = uint32(freq)
		it.Decoded++
		it.Skips++
	}
	for it.doc < target {
		if !it.Next() {
			return false
		}
	}
	return true
}
