package prefixseek

import (
	"bytes"
	"encoding/binary"
)

// PrefixIterator iterates all key/value pairs sharing a given prefix,
// in key order. It uses the SSTable index block to binary-search directly
// to the first data block that may contain the prefix, skips all preceding
// blocks, and stops immediately (boundary truncation) at the first key
// that no longer carries the prefix — e.g. when the next tenant begins.
//
// Keys and values returned by Key/Value are zero-copy subslices of the
// table image; they must not be mutated.
type PrefixIterator struct {
	r      *Reader
	prefix []byte

	blockIdx int    // next data block index to load
	block    []byte // remaining undecoded bytes of the current block
	done     bool

	key   []byte
	value []byte

	// BlocksSkipped counts index entries bypassed by the binary search.
	BlocksSkipped int
	// BlocksRead counts data blocks actually decoded.
	BlocksRead int
}

// PrefixSeek returns an iterator positioned at the first key >= prefix
// that carries prefix. It binary-searches the index block so all blocks
// before the prefix range are skipped without being read.
func (r *Reader) PrefixSeek(prefix []byte) *PrefixIterator {
	it := &PrefixIterator{r: r, prefix: append([]byte(nil), prefix...)}
	blk, skipped := r.locateBlock(prefix)
	it.blockIdx = blk
	it.BlocksSkipped = skipped
	if blk >= len(r.index) {
		it.done = true
	}
	return it
}

// Next advances to the next entry carrying the prefix. It returns false
// when the prefix range is exhausted or a key beyond the prefix boundary
// is encountered.
func (it *PrefixIterator) Next() bool {
	if it.done {
		return false
	}
	for {
		if len(it.block) == 0 {
			if it.blockIdx >= len(it.r.index) {
				it.done = true
				return false
			}
			it.block = it.r.block(it.blockIdx)
			it.blockIdx++
			it.BlocksRead++
		}
		key, value, rest, ok := decodeEntry(it.block)
		if !ok {
			it.done = true
			return false
		}
		it.block = rest
		if bytes.Compare(key, it.prefix) < 0 {
			continue // still before the prefix range inside the first block
		}
		if !bytes.HasPrefix(key, it.prefix) {
			// Boundary truncation: crossed into the next tenant/range.
			it.done = true
			return false
		}
		it.key = key
		it.value = value
		return true
	}
}

// Key returns the current key (zero-copy).
func (it *PrefixIterator) Key() []byte { return it.key }

// Value returns the current value (zero-copy).
func (it *PrefixIterator) Value() []byte { return it.value }

// decodeEntry reads one (key, value) pair from the head of buf and
// returns the remaining bytes.
func decodeEntry(buf []byte) (key, value, rest []byte, ok bool) {
	klen, n := binary.Uvarint(buf)
	if n <= 0 {
		return nil, nil, nil, false
	}
	buf = buf[n:]
	vlen, n := binary.Uvarint(buf)
	if n <= 0 {
		return nil, nil, nil, false
	}
	buf = buf[n:]
	if uint64(len(buf)) < klen+vlen {
		return nil, nil, nil, false
	}
	key = buf[:klen]
	value = buf[klen : klen+vlen]
	rest = buf[klen+vlen:]
	return key, value, rest, true
}
