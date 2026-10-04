package blockmatch

import (
	"crypto/sha256"
	"fmt"
)

// BlockRef identifies a verified block inside the reference source.
type BlockRef struct {
	Offset int64
	Strong [sha256.Size]byte
}

// Index maps weak rolling fingerprints of the reference source to candidate
// blocks. A weak hit is only a candidate: callers must confirm it with the
// strong digest and a byte-for-byte comparison before trusting a match.
//
// The same source can be indexed at several block lengths, so a Matcher may
// switch block size mid-stream.
type Index struct {
	source   []byte
	weakMask uint32
	sizes    map[int]map[uint32][]BlockRef // blockSize -> weak -> candidates
}

// NewIndex builds an empty index over source. weakBits selects how many low
// bits of the 32-bit rolling fingerprint are used as the lookup key; small
// values force frequent collisions (useful for tests).
func NewIndex(source []byte, weakBits int) (*Index, error) {
	if weakBits < 1 || weakBits > 32 {
		return nil, fmt.Errorf("blockmatch: weakBits %d out of range [1,32]", weakBits)
	}
	mask := uint32(0xffffffff)
	if weakBits < 32 {
		mask = 1<<uint(weakBits) - 1
	}
	return &Index{
		source:   source,
		weakMask: mask,
		sizes:    make(map[int]map[uint32][]BlockRef),
	}, nil
}

// AddBlockSize indexes the source at the given (non-overlapping) block
// length. It is idempotent.
func (idx *Index) AddBlockSize(blockSize int) error {
	if blockSize < 1 {
		return fmt.Errorf("blockmatch: invalid block size %d", blockSize)
	}
	if _, ok := idx.sizes[blockSize]; ok {
		return nil
	}
	table := make(map[uint32][]BlockRef)
	for off := 0; off+blockSize <= len(idx.source); off += blockSize {
		block := idx.source[off : off+blockSize]
		var h rollingHash
		for _, b := range block {
			h.append(b)
		}
		weak := h.value() & idx.weakMask
		table[weak] = append(table[weak], BlockRef{
			Offset: int64(off),
			Strong: sha256.Sum256(block),
		})
	}
	idx.sizes[blockSize] = table
	return nil
}

// candidates returns the blocks whose weak fingerprint equals weak.
func (idx *Index) candidates(blockSize int, weak uint32) []BlockRef {
	return idx.sizes[blockSize][weak]
}
