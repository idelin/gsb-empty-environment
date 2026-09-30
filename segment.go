// Package sstdict implements an immutable, block-compressed sorted string
// table with prefix sharing, per-block CRC checksums, random access by
// ordinal, and byte-prefix range queries.
//
// On-disk / in-memory layout (all integers little-endian unless varint):
//
//	Header (28 bytes, fixed):
//	  magic     [4]byte   "SST1"
//	  blockSize uint32    max entries per block
//	  numBlocks uint32
//	  numWords  uint64
//	  indexOff  uint32    absolute offset of the index section
//	  indexLen  uint32    byte length of the index section
//
//	Data section (immediately after header): sequence of blocks.
//	Block:
//	  payloadLen uint32
//	  crc32      uint32   IEEE checksum of payload
//	  payload:
//	    uvarint entryCount            (must be <= blockSize)
//	    entry[0]:   uvarint wordLen | word bytes        (restart: full word)
//	    entry[i>0]: uvarint prefixLen | uvarint suffixLen | suffix bytes
//
//	Index section (at indexOff): one entry per block.
//	  uvarint len(firstWord) | firstWord bytes | uvarint blockOffset
//	  blockOffset is relative to the start of the data section.
//
// Every block is self-contained: its first word is stored in full and no
// entry may reference content of a previous block. All length fields are
// validated against the remaining payload (and prefixLen against the
// block-local previous word) BEFORE any allocation happens.
package sstdict

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
)

const (
	magic        = "SST1"
	headerSize   = 28
	maxBlockSize = 1 << 20
)

var (
	ErrCorrupt   = errors.New("sstdict: corrupt segment")
	ErrDuplicate = errors.New("sstdict: duplicate word")
	ErrUnsorted  = errors.New("sstdict: input not sorted")
	ErrRange     = errors.New("sstdict: index out of range")
)

type indexEntry struct {
	first  string
	offset uint32 // relative to data section start
}

func appendUvarint(b []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(b, tmp[:n]...)
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// Builder constructs one immutable Segment from a strictly increasing
// sequence of words. Duplicate words are rejected (ErrDuplicate); that is
// the duplicate policy for a single batch. An empty string is a legal
// first word.
type Builder struct {
	blockSize int
	data      []byte
	index     []indexEntry
	payload   []byte
	blockN    int
	count     uint64
	prev      string
	hasPrev   bool
}

func NewBuilder(blockSize int) *Builder {
	if blockSize <= 0 || blockSize > maxBlockSize {
		panic(fmt.Sprintf("sstdict: bad block size %d", blockSize))
	}
	return &Builder{blockSize: blockSize}
}

// Add appends word, which must be strictly greater than every previously
// added word (byte-wise order). Returns ErrDuplicate on repeats and
// ErrUnsorted on out-of-order input.
func (b *Builder) Add(word string) error {
	if b.hasPrev {
		if word == b.prev {
			return fmt.Errorf("%w: %q", ErrDuplicate, word)
		}
		if word < b.prev {
			return fmt.Errorf("%w: %q after %q", ErrUnsorted, word, b.prev)
		}
	}
	if b.blockN == 0 {
		// Restart entry: full word, no reference to any previous block.
		b.payload = appendUvarint(b.payload, uint64(len(word)))
		b.payload = append(b.payload, word...)
		b.index = append(b.index, indexEntry{first: word, offset: uint32(len(b.data))})
	} else {
		shared := commonPrefixLen(b.prev, word)
		suffix := word[shared:]
		b.payload = appendUvarint(b.payload, uint64(shared))
		b.payload = appendUvarint(b.payload, uint64(len(suffix)))
		b.payload = append(b.payload, suffix...)
	}
	b.prev = word
	b.hasPrev = true
	b.blockN++
	b.count++
	if b.blockN == b.blockSize {
		b.flushBlock()
	}
	return nil
}

func (b *Builder) flushBlock() {
	if b.blockN == 0 {
		return
	}
	payload := appendUvarint(nil, uint64(b.blockN))
	payload = append(payload, b.payload...)
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(hdr[4:8], crc32.ChecksumIEEE(payload))
	b.data = append(b.data, hdr[:]...)
	b.data = append(b.data, payload...)
	b.payload = b.payload[:0]
	b.blockN = 0
}

// Build finalizes the segment and validates it by re-opening it.
func (b *Builder) Build() (*Segment, error) {
	if b.count == 0 {
		return nil, errors.New("sstdict: cannot build empty segment")
	}
	b.flushBlock()
	var hdr [headerSize]byte
	copy(hdr[0:4], magic)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(b.blockSize))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(b.index)))
	binary.LittleEndian.PutUint64(hdr[12:20], b.count)
	indexOff := headerSize + len(b.data)
	binary.LittleEndian.PutUint32(hdr[20:24], uint32(indexOff))
	var index []byte
	for _, e := range b.index {
		index = appendUvarint(index, uint64(len(e.first)))
		index = append(index, e.first...)
		index = appendUvarint(index, uint64(e.offset))
	}
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(len(index)))
	out := make([]byte, 0, indexOff+len(index))
	out = append(out, hdr[:]...)
	out = append(out, b.data...)
	out = append(out, index...)
	return OpenSegment(out)
}

// Segment is an immutable sorted string table.
type Segment struct {
	data       []byte
	index      []indexEntry
	blockSize  int
	numWords   uint64
	indexBytes int
	id         uint64
}

// OpenSegment parses and validates the segment header and sparse index.
// Block payloads are verified lazily on read (CRC + strict length checks).
func OpenSegment(buf []byte) (*Segment, error) {
	if len(buf) < headerSize {
		return nil, fmt.Errorf("%w: header truncated", ErrCorrupt)
	}
	if string(buf[0:4]) != magic {
		return nil, fmt.Errorf("%w: bad magic %q", ErrCorrupt, buf[0:4])
	}
	blockSize := binary.LittleEndian.Uint32(buf[4:8])
	numBlocks := binary.LittleEndian.Uint32(buf[8:12])
	numWords := binary.LittleEndian.Uint64(buf[12:20])
	indexOff := binary.LittleEndian.Uint32(buf[20:24])
	indexLen := binary.LittleEndian.Uint32(buf[24:28])
	if blockSize == 0 || blockSize > maxBlockSize {
		return nil, fmt.Errorf("%w: bad block size %d", ErrCorrupt, blockSize)
	}
	if uint64(indexOff) < headerSize || uint64(indexOff)+uint64(indexLen) != uint64(len(buf)) {
		return nil, fmt.Errorf("%w: bad index bounds off=%d len=%d total=%d", ErrCorrupt, indexOff, indexLen, len(buf))
	}
	dataLen := indexOff - headerSize
	p := buf[indexOff:]
	index := make([]indexEntry, 0, numBlocks)
	prevOff := uint64(0)
	for i := uint32(0); i < numBlocks; i++ {
		wlen, n := binary.Uvarint(p)
		if n <= 0 {
			return nil, fmt.Errorf("%w: bad index word length", ErrCorrupt)
		}
		p = p[n:]
		if wlen > uint64(len(p)) {
			return nil, fmt.Errorf("%w: index word length %d exceeds remaining %d", ErrCorrupt, wlen, len(p))
		}
		first := string(p[:wlen])
		p = p[wlen:]
		off, n := binary.Uvarint(p)
		if n <= 0 {
			return nil, fmt.Errorf("%w: bad index offset", ErrCorrupt)
		}
		p = p[n:]
		if off >= uint64(dataLen) && !(off == 0 && i == 0) {
			return nil, fmt.Errorf("%w: block offset %d beyond data section %d", ErrCorrupt, off, dataLen)
		}
		if i > 0 && off <= prevOff {
			return nil, fmt.Errorf("%w: block offsets not increasing", ErrCorrupt)
		}
		prevOff = off
		index = append(index, indexEntry{first: first, offset: uint32(off)})
	}
	if len(p) != 0 {
		return nil, fmt.Errorf("%w: trailing bytes in index", ErrCorrupt)
	}
	if numBlocks > 0 && numWords == 0 {
		return nil, fmt.Errorf("%w: blocks but no words", ErrCorrupt)
	}
	return &Segment{
		data:       buf,
		index:      index,
		blockSize:  int(blockSize),
		numWords:   numWords,
		indexBytes: int(indexLen),
	}, nil
}

// decodeBlock decodes the block at the given data-section-relative offset.
// The CRC is verified first; a mismatch means the block is corrupt and no
// word from it (or derived from it) is returned.
func (s *Segment) decodeBlock(off uint32) ([]string, error) {
	abs := uint64(headerSize) + uint64(off)
	if abs+8 > uint64(len(s.data)) {
		return nil, fmt.Errorf("%w: block header truncated at %d", ErrCorrupt, off)
	}
	payloadLen := binary.LittleEndian.Uint32(s.data[abs:])
	crc := binary.LittleEndian.Uint32(s.data[abs+4:])
	if abs+8+uint64(payloadLen) > uint64(len(s.data)) {
		return nil, fmt.Errorf("%w: block payload length %d overruns segment", ErrCorrupt, payloadLen)
	}
	payload := s.data[abs+8 : abs+8+uint64(payloadLen)]
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, fmt.Errorf("%w: crc mismatch in block at %d", ErrCorrupt, off)
	}
	return decodePayload(payload, s.blockSize)
}

// decodePayload decodes one block payload. Every length field is checked
// against the remaining payload (and prefixLen against the block-local
// previous word) before any allocation, so a corrupt length can neither
// trigger a huge allocation nor reference content outside this block.
func decodePayload(p []byte, blockSize int) ([]string, error) {
	n, c := binary.Uvarint(p)
	if c <= 0 {
		return nil, fmt.Errorf("%w: bad entry count", ErrCorrupt)
	}
	p = p[c:]
	if n == 0 || n > uint64(blockSize) {
		return nil, fmt.Errorf("%w: entry count %d exceeds block size %d", ErrCorrupt, n, blockSize)
	}
	words := make([]string, 0, n)
	wlen, c := binary.Uvarint(p)
	if c <= 0 {
		return nil, fmt.Errorf("%w: bad restart word length", ErrCorrupt)
	}
	p = p[c:]
	if wlen > uint64(len(p)) {
		return nil, fmt.Errorf("%w: restart word length %d exceeds remaining %d", ErrCorrupt, wlen, len(p))
	}
	prev := string(p[:wlen])
	p = p[wlen:]
	words = append(words, prev)
	for i := uint64(1); i < n; i++ {
		plen, c := binary.Uvarint(p)
		if c <= 0 {
			return nil, fmt.Errorf("%w: bad prefix length", ErrCorrupt)
		}
		p = p[c:]
		slen, c := binary.Uvarint(p)
		if c <= 0 {
			return nil, fmt.Errorf("%w: bad suffix length", ErrCorrupt)
		}
		p = p[c:]
		// Reject corrupt lengths BEFORE allocating: plen may not exceed
		// the block-local previous word, slen may not exceed the payload.
		if plen > uint64(len(prev)) {
			return nil, fmt.Errorf("%w: prefix length %d exceeds previous word length %d", ErrCorrupt, plen, len(prev))
		}
		if slen > uint64(len(p)) {
			return nil, fmt.Errorf("%w: suffix length %d exceeds remaining payload %d", ErrCorrupt, slen, len(p))
		}
		w := make([]byte, plen+slen)
		copy(w, prev[:plen])
		copy(w[plen:], p[:slen])
		p = p[slen:]
		prev = string(w)
		words = append(words, prev)
	}
	if len(p) != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes in block", ErrCorrupt, len(p))
	}
	return words, nil
}

// NumWords returns the number of words in the segment.
func (s *Segment) NumWords() uint64 { return s.numWords }

// Get returns the k-th word (0-based). Because every block except the
// last holds exactly blockSize words, the target block is located
// arithmetically and at most blockSize entries are decoded.
func (s *Segment) Get(k uint64, st *Stats) (string, error) {
	if k >= s.numWords {
		return "", fmt.Errorf("%w: %d >= %d", ErrRange, k, s.numWords)
	}
	bi := int(k) / s.blockSize
	pos := int(k) % s.blockSize
	words, err := s.decodeBlock(s.index[bi].offset)
	if err != nil {
		return "", err
	}
	if pos >= len(words) {
		return "", fmt.Errorf("%w: block %d has %d entries, need %d", ErrCorrupt, bi, len(words), pos+1)
	}
	if st != nil {
		st.observeRandomRead(pos + 1)
	}
	return words[pos], nil
}

// PrefixRange returns all words starting with prefix, in order. Prefix
// matching is byte-wise (UTF-8 agnostic); returned words are always the
// complete stored strings.
func (s *Segment) PrefixRange(prefix string, st *Stats) ([]string, error) {
	// First block whose restart word is > prefix; start one block earlier.
	bi := sort.Search(len(s.index), func(i int) bool { return s.index[i].first > prefix }) - 1
	if bi < 0 {
		bi = 0
	}
	var out []string
	for ; bi < len(s.index); bi++ {
		words, err := s.decodeBlock(s.index[bi].offset)
		if err != nil {
			return nil, err
		}
		if st != nil {
			st.observeScan(len(words))
		}
		for _, w := range words {
			if w < prefix {
				continue
			}
			if !strings.HasPrefix(w, prefix) {
				// Sorted order: no later word can carry this prefix.
				return out, nil
			}
			out = append(out, w)
		}
	}
	return out, nil
}

// Iterator streams all words of a segment in order, one block at a time.
type Iterator struct {
	seg   *Segment
	bi    int
	words []string
	wi    int
	err   error
}

func (s *Segment) Iterator() *Iterator { return &Iterator{seg: s} }

func (it *Iterator) Next() (string, bool) {
	for {
		if it.err != nil {
			return "", false
		}
		if it.wi < len(it.words) {
			w := it.words[it.wi]
			it.wi++
			return w, true
		}
		if it.bi >= len(it.seg.index) {
			return "", false
		}
		words, err := it.seg.decodeBlock(it.seg.index[it.bi].offset)
		if err != nil {
			it.err = err
			return "", false
		}
		it.bi++
		it.words = words
		it.wi = 0
	}
}

func (it *Iterator) Err() error { return it.err }

// Layout reports the real byte occupancy of the encoded segment.
type Layout struct {
	HeaderBytes int
	DataBytes   int
	IndexBytes  int
	TotalBytes  int
	NumWords    uint64
	NumBlocks   int
	// NaiveBytes is what an uncompressed length-prefixed table would use:
	// 4 length bytes per word plus the raw word bytes.
	NaiveBytes int
}

func (s *Segment) Layout() Layout {
	naive := 0
	it := s.Iterator()
	for w, ok := it.Next(); ok; w, ok = it.Next() {
		naive += 4 + len(w)
	}
	return Layout{
		HeaderBytes: headerSize,
		DataBytes:   len(s.data) - headerSize - s.indexBytes,
		IndexBytes:  s.indexBytes,
		TotalBytes:  len(s.data),
		NumWords:    s.numWords,
		NumBlocks:   len(s.index),
		NaiveBytes:  naive,
	}
}
