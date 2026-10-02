// Package prefixseek implements an LSM-style SSTable with an Index Block
// that supports prefix Seek: binary-search the in-memory index to jump
// directly to the first data block that may contain the prefix, then scan
// sequentially with zero-copy key/value slices and stop as soon as the key
// leaves the prefix range (boundary truncation).
package prefixseek

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
)

const (
	// DefaultBlockSize is the target data-block size in bytes.
	DefaultBlockSize = 4096

	magic      uint64 = 0x505345454b303031 // "PSEEK001"
	footerSize        = 24                 // indexOffset(8) + indexSize(8) + magic(8)
)

// ErrUnsortedKeys is returned by Builder.Add when keys are not increasing.
var ErrUnsortedKeys = errors.New("prefixseek: keys must be added in strictly increasing order")

// appendUvarint appends the uvarint encoding of x to buf
// (binary.AppendUvarint does not exist in older toolchains).
func appendUvarint(buf []byte, x uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], x)
	return append(buf, tmp[:n]...)
}

// indexEntry maps the last key of a data block to its file location.
type indexEntry struct {
	sepKey []byte // last (greatest) key stored in the block
	offset uint64
	size   uint64
}

// ---------------------------------------------------------------------------
// Builder
// ---------------------------------------------------------------------------

// Builder writes a sorted run of key/value pairs into an SSTable file:
//
//	[DataBlock]* [IndexBlock] [Footer]
//
// DataBlock  : (uvarint klen | key | uvarint vlen | value)*
// IndexBlock : (uvarint klen | sepKey | uvarint offset | uvarint size)*
// Footer     : indexOffset uint64 | indexSize uint64 | magic uint64
type Builder struct {
	f         *os.File
	blockSize int

	buf     []byte // current, not-yet-flushed data block
	offset  uint64 // file offset of the next write
	index   []indexEntry
	lastKey []byte
	count   int
	done    bool
}

// NewBuilder creates an SSTable builder writing to path.
func NewBuilder(path string, blockSize int) (*Builder, error) {
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &Builder{f: f, blockSize: blockSize}, nil
}

// Add appends a key/value pair. Keys MUST be added in strictly increasing
// lexicographic order (it is a sorted run, like an LSM flush/compaction).
func (b *Builder) Add(key, value []byte) error {
	if b.done {
		return errors.New("prefixseek: Add after Finish")
	}
	if b.lastKey != nil && bytes.Compare(key, b.lastKey) <= 0 {
		return ErrUnsortedKeys
	}
	b.buf = appendUvarint(b.buf, uint64(len(key)))
	b.buf = append(b.buf, key...)
	b.buf = appendUvarint(b.buf, uint64(len(value)))
	b.buf = append(b.buf, value...)

	b.lastKey = append(b.lastKey[:0], key...)
	b.count++
	if len(b.buf) >= b.blockSize {
		return b.flushBlock()
	}
	return nil
}

func (b *Builder) flushBlock() error {
	if len(b.buf) == 0 {
		return nil
	}
	sep := make([]byte, len(b.lastKey))
	copy(sep, b.lastKey)
	b.index = append(b.index, indexEntry{sepKey: sep, offset: b.offset, size: uint64(len(b.buf))})
	if _, err := b.f.Write(b.buf); err != nil {
		return err
	}
	b.offset += uint64(len(b.buf))
	b.buf = b.buf[:0]
	return nil
}

// Finish flushes the pending block, writes the index block and footer,
// and closes the file.
func (b *Builder) Finish() error {
	if b.done {
		return errors.New("prefixseek: Finish called twice")
	}
	b.done = true
	defer b.f.Close()

	if err := b.flushBlock(); err != nil {
		return err
	}
	indexOff := b.offset
	var ib []byte
	for _, e := range b.index {
		ib = appendUvarint(ib, uint64(len(e.sepKey)))
		ib = append(ib, e.sepKey...)
		ib = appendUvarint(ib, e.offset)
		ib = appendUvarint(ib, e.size)
	}
	if _, err := b.f.Write(ib); err != nil {
		return err
	}
	var footer [footerSize]byte
	binary.BigEndian.PutUint64(footer[0:8], indexOff)
	binary.BigEndian.PutUint64(footer[8:16], uint64(len(ib)))
	binary.BigEndian.PutUint64(footer[16:24], magic)
	_, err := b.f.Write(footer[:])
	return err
}

// ---------------------------------------------------------------------------
// Reader
// ---------------------------------------------------------------------------

// Reader opens an SSTable, loads its Index Block into memory and serves
// prefix iterators. BlocksRead counts how many data blocks were fetched
// from disk; it is exported so tests and demos can verify that Seek skips
// irrelevant blocks.
type Reader struct {
	f          *os.File
	index      []indexEntry
	BlocksRead int
}

// Open opens the SSTable at path and reads its index block.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() < footerSize {
		f.Close()
		return nil, errors.New("prefixseek: file too small")
	}
	var footer [footerSize]byte
	if _, err := f.ReadAt(footer[:], st.Size()-footerSize); err != nil {
		f.Close()
		return nil, err
	}
	if binary.BigEndian.Uint64(footer[16:24]) != magic {
		f.Close()
		return nil, errors.New("prefixseek: bad magic, not an SSTable")
	}
	indexOff := binary.BigEndian.Uint64(footer[0:8])
	indexLen := binary.BigEndian.Uint64(footer[8:16])

	raw := make([]byte, indexLen)
	if _, err := f.ReadAt(raw, int64(indexOff)); err != nil {
		f.Close()
		return nil, err
	}
	var index []indexEntry
	for len(raw) > 0 {
		klen, n := binary.Uvarint(raw)
		if n <= 0 {
			f.Close()
			return nil, errors.New("prefixseek: corrupt index block")
		}
		raw = raw[n:]
		sep := make([]byte, klen)
		copy(sep, raw[:klen])
		raw = raw[klen:]
		off, n := binary.Uvarint(raw)
		raw = raw[n:]
		sz, n := binary.Uvarint(raw)
		raw = raw[n:]
		index = append(index, indexEntry{sepKey: sep, offset: off, size: sz})
	}
	return &Reader{f: f, index: index}, nil
}

// Close closes the underlying file.
func (r *Reader) Close() error { return r.f.Close() }

// NumBlocks returns the number of data blocks in the table.
func (r *Reader) NumBlocks() int { return len(r.index) }

// readBlock fetches block i from disk. The returned slice is owned by the
// caller and never copied again: iterator keys/values are sub-slices of it.
func (r *Reader) readBlock(i int) ([]byte, error) {
	e := r.index[i]
	buf := make([]byte, e.size)
	if _, err := r.f.ReadAt(buf, int64(e.offset)); err != nil {
		return nil, err
	}
	r.BlocksRead++
	return buf, nil
}

// findBlock returns the index of the first block whose separator key is
// >= target, i.e. the first block that may contain target.
func (r *Reader) findBlock(target []byte) int {
	return sort.Search(len(r.index), func(i int) bool {
		return bytes.Compare(r.index[i].sepKey, target) >= 0
	})
}

// ---------------------------------------------------------------------------
// Prefix iterator
// ---------------------------------------------------------------------------

// Iterator scans exactly the keys that start with a given prefix.
// Key() and Value() return zero-copy sub-slices of the raw block buffer;
// they stay valid until the next call to Next().
type Iterator struct {
	r      *Reader
	prefix []byte

	blockIdx int    // current block index, -1 before first load
	block    []byte // raw buffer of the current block
	pos      int    // parse position inside block
	endOfBlk bool

	key, val []byte
	valid    bool
	err      error

	// StopBlock records the block index where the scan terminated, so
	// tests can prove the iterator stopped early instead of running to EOF.
	StopBlock int
}

// PrefixSeek positions an iterator at the first key >= prefix and yields
// every key that starts with prefix, stopping immediately at the first key
// outside the prefix range (boundary truncation).
func (r *Reader) PrefixSeek(prefix []byte) *Iterator {
	it := &Iterator{r: r, prefix: prefix, blockIdx: -1, StopBlock: -1}
	it.seek()
	return it
}

// seek binary-searches the index block for the first candidate block and
// positions the iterator on the first key >= prefix.
func (it *Iterator) seek() {
	idx := it.r.findBlock(it.prefix)
	if idx >= len(it.r.index) {
		return // prefix is beyond the last key of the table
	}
	if !it.loadBlock(idx) {
		return
	}
	// Linear scan inside the first block until key >= prefix.
	for it.parseNext() {
		if bytes.Compare(it.key, it.prefix) >= 0 {
			it.checkPrefix()
			return
		}
	}
	it.advanceBlock()
}

// loadBlock reads block idx into memory.
func (it *Iterator) loadBlock(idx int) bool {
	buf, err := it.r.readBlock(idx)
	if err != nil {
		it.err = err
		return false
	}
	it.blockIdx = idx
	it.block = buf
	it.pos = 0
	it.endOfBlk = false
	return true
}

// parseNext decodes the next entry of the current block into it.key/it.val
// as zero-copy sub-slices. Returns false when the block is exhausted.
func (it *Iterator) parseNext() bool {
	if it.endOfBlk {
		return false
	}
	buf := it.block[it.pos:]
	klen, n := binary.Uvarint(buf)
	if n <= 0 || int(klen) > len(buf)-n {
		it.err = fmt.Errorf("prefixseek: corrupt data block %d", it.blockIdx)
		return false
	}
	buf = buf[n:]
	it.key = buf[:klen]
	buf = buf[klen:]
	vlen, n := binary.Uvarint(buf)
	if n <= 0 || int(vlen) > len(buf)-n {
		it.err = fmt.Errorf("prefixseek: corrupt data block %d", it.blockIdx)
		return false
	}
	buf = buf[n:]
	it.val = buf[:vlen]
	it.pos = len(it.block) - (len(buf) - int(vlen))
	if it.pos >= len(it.block) {
		it.endOfBlk = true
	}
	return true
}

// checkPrefix validates the current key against the prefix boundary.
// Keys are sorted, so the first key >= prefix that no longer carries the
// prefix marks the end of the range: stop immediately.
func (it *Iterator) checkPrefix() {
	if bytes.HasPrefix(it.key, it.prefix) {
		it.valid = true
		return
	}
	it.valid = false
	it.StopBlock = it.blockIdx
}

// advanceBlock moves to the next data block and continues the scan.
func (it *Iterator) advanceBlock() {
	for {
		next := it.blockIdx + 1
		if next >= len(it.r.index) {
			it.StopBlock = it.blockIdx
			return // ran off the end of the table
		}
		// Boundary truncation at block granularity: if the smallest
		// possible key of the next block (previous separator) is already
		// out of range we could stop, but separator keys are block-last
		// keys, so we must read the block and check its first key.
		if !it.loadBlock(next) {
			return
		}
		if it.parseNext() {
			it.checkPrefix()
			return
		}
	}
}

// Valid reports whether the iterator points at a key inside the prefix.
func (it *Iterator) Valid() bool { return it.valid }

// Key returns the current key (zero-copy, valid until Next).
func (it *Iterator) Key() []byte { return it.key }

// Value returns the current value (zero-copy, valid until Next).
func (it *Iterator) Value() []byte { return it.val }

// Err returns the first error encountered during iteration, if any.
func (it *Iterator) Err() error { return it.err }

// Next advances to the next entry, stopping at the prefix boundary.
func (it *Iterator) Next() {
	if !it.valid {
		return
	}
	if it.parseNext() {
		it.checkPrefix()
		return
	}
	it.advanceBlock()
}
