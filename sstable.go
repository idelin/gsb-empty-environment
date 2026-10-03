// Package prefixseek implements an LSM-style SSTable with an index block,
// supporting prefix Seek with binary-search block location and boundary
// truncation. Keys returned by iterators are zero-copy subslices of the
// underlying table buffer.
package prefixseek

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

const (
	// DefaultBlockSize is the target uncompressed data-block size in bytes.
	DefaultBlockSize = 4096

	footerLen   = 24                 // indexOffset u64 | indexLen u64 | magic u64
	magicNumber = 0x505245464958534b // "PRFIXSK"
)

var (
	ErrCorrupted   = errors.New("prefixseek: corrupted sstable")
	ErrNotFinished = errors.New("prefixseek: builder not finished")
)

// indexEntry maps the last key of a data block to its location in the file.
type indexEntry struct {
	lastKey []byte // zero-copy subslice of the table buffer
	offset  uint64
	size    uint32
}

// Builder writes sorted key/value records into an SSTable buffer.
// Keys MUST be added in strictly increasing order.
type Builder struct {
	buf        bytes.Buffer
	block      bytes.Buffer
	blockSize  int
	index      []indexEntry
	lastKey    []byte
	blockFirst []byte
	n          int
	finished   bool
}

// NewBuilder creates a Builder with the given target data-block size.
// If blockSize <= 0, DefaultBlockSize is used.
func NewBuilder(blockSize int) *Builder {
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	return &Builder{blockSize: blockSize}
}

// Add appends a key/value record. Keys must be added in ascending order.
func (b *Builder) Add(key, value []byte) error {
	if b.finished {
		return errors.New("prefixseek: Add after Finish")
	}
	if b.n > 0 && bytes.Compare(b.lastKey, key) >= 0 {
		return fmt.Errorf("prefixseek: keys out of order: %q after %q", key, b.lastKey)
	}
	if b.block.Len() == 0 {
		b.blockFirst = append(b.blockFirst[:0], key...)
	}
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], uint64(len(key)))
	b.block.Write(tmp[:n])
	n = binary.PutUvarint(tmp[:], uint64(len(value)))
	b.block.Write(tmp[:n])
	b.block.Write(key)
	b.block.Write(value)
	b.lastKey = append(b.lastKey[:0], key...)
	b.n++
	if b.block.Len() >= b.blockSize {
		b.flushBlock()
	}
	return nil
}

func (b *Builder) flushBlock() {
	if b.block.Len() == 0 {
		return
	}
	offset := uint64(b.buf.Len())
	size := uint32(b.block.Len())
	b.buf.Write(b.block.Bytes())
	b.block.Reset()
	lastKey := append([]byte(nil), b.lastKey...)
	b.index = append(b.index, indexEntry{lastKey: lastKey, offset: offset, size: size})
}

// Finish seals the table: flushes the pending block, writes the index block
// and the footer, and returns the complete SSTable image.
func (b *Builder) Finish() []byte {
	b.flushBlock()
	indexOffset := uint64(b.buf.Len())
	var tmp [binary.MaxVarintLen64]byte
	for _, e := range b.index {
		n := binary.PutUvarint(tmp[:], uint64(len(e.lastKey)))
		b.buf.Write(tmp[:n])
		b.buf.Write(e.lastKey)
		n = binary.PutUvarint(tmp[:], e.offset)
		b.buf.Write(tmp[:n])
		n = binary.PutUvarint(tmp[:], uint64(e.size))
		b.buf.Write(tmp[:n])
	}
	indexLen := uint64(b.buf.Len()) - indexOffset
	var footer [footerLen]byte
	binary.LittleEndian.PutUint64(footer[0:8], indexOffset)
	binary.LittleEndian.PutUint64(footer[8:16], indexLen)
	binary.LittleEndian.PutUint64(footer[16:24], magicNumber)
	b.buf.Write(footer[:])
	b.finished = true
	return b.buf.Bytes()
}

// NumRecords returns how many records were added.
func (b *Builder) NumRecords() int { return b.n }

// Reader provides read access to an SSTable image held in memory.
// All keys and values returned are zero-copy subslices of the image.
type Reader struct {
	data  []byte
	index []indexEntry
}

// Open parses an SSTable image. The image must remain alive for the
// lifetime of the Reader and any iterators created from it.
func Open(data []byte) (*Reader, error) {
	if len(data) < footerLen {
		return nil, ErrCorrupted
	}
	footer := data[len(data)-footerLen:]
	if binary.LittleEndian.Uint64(footer[16:24]) != magicNumber {
		return nil, ErrCorrupted
	}
	indexOffset := binary.LittleEndian.Uint64(footer[0:8])
	indexLen := binary.LittleEndian.Uint64(footer[8:16])
	if indexOffset+indexLen > uint64(len(data)-footerLen) {
		return nil, ErrCorrupted
	}
	r := &Reader{data: data}
	idx := data[indexOffset : indexOffset+indexLen]
	for len(idx) > 0 {
		klen, n := binary.Uvarint(idx)
		if n <= 0 || uint64(len(idx[n:])) < klen {
			return nil, ErrCorrupted
		}
		idx = idx[n:]
		lastKey := idx[:klen]
		idx = idx[klen:]
		off, n := binary.Uvarint(idx)
		if n <= 0 {
			return nil, ErrCorrupted
		}
		idx = idx[n:]
		size, n := binary.Uvarint(idx)
		if n <= 0 {
			return nil, ErrCorrupted
		}
		idx = idx[n:]
		if off+size > uint64(len(data)) {
			return nil, ErrCorrupted
		}
		r.index = append(r.index, indexEntry{lastKey: lastKey, offset: off, size: uint32(size)})
	}
	return r, nil
}

// NumBlocks returns the number of data blocks in the table.
func (r *Reader) NumBlocks() int { return len(r.index) }

// locateBlock binary-searches the index block for the first data block
// whose last key is >= key, i.e. the first block that may contain key.
// It returns the block index and how many blocks were skipped.
func (r *Reader) locateBlock(key []byte) (int, int) {
	i := sort.Search(len(r.index), func(i int) bool {
		return bytes.Compare(r.index[i].lastKey, key) >= 0
	})
	return i, i
}

// block returns the raw bytes of data block i (zero-copy).
func (r *Reader) block(i int) []byte {
	e := r.index[i]
	return r.data[e.offset : e.offset+uint64(e.size)]
}

// scanBlock iterates the entries of a raw block. Returning false from fn
// stops the scan early.
func scanBlock(block []byte, fn func(key, value []byte) bool) {
	for len(block) > 0 {
		klen, n := binary.Uvarint(block)
		if n <= 0 {
			return
		}
		block = block[n:]
		vlen, n := binary.Uvarint(block)
		if n <= 0 {
			return
		}
		block = block[n:]
		if uint64(len(block)) < klen+vlen {
			return
		}
		key := block[:klen]
		value := block[klen : klen+vlen]
		block = block[klen+vlen:]
		if !fn(key, value) {
			return
		}
	}
}
