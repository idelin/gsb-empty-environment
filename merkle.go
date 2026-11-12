package netsec

import (
	"crypto/subtle"
	"fmt"
	"hash"
	"io"
)

// merkleTree stores the leaf digests and every packed hash-block digest.
// levels[0] contains the data block hashes; levels[len-1][0] is the root.
type merkleTree struct {
	algorithm     Algorithm
	saltPrefix    []byte
	dataSize      uint64
	numDataBlocks int
	levels        [][][]byte
	root          []byte
}

type blockReader func(index int) ([]byte, error)

func rootHashFromData(data, salt []byte, alg Algorithm) ([]byte, error) {
	if err := alg.validate(); err != nil {
		return nil, err
	}
	if len(salt) > SaltSize {
		return nil, fmt.Errorf("%w: length=%d", ErrInvalidSalt, len(salt))
	}

	tree, err := buildMerkleTree(uint64(len(data)), alg, salt, func(index int) ([]byte, error) {
		start := index * BlockSize
		remaining := len(data) - start
		if remaining <= 0 {
			return nil, ErrBlockIndex
		}
		if remaining > BlockSize {
			remaining = BlockSize
		}
		return data[start : start+remaining], nil
	})
	if err != nil {
		return nil, err
	}
	return tree.root, nil
}

func buildMerkleTree(dataSize uint64, alg Algorithm, salt []byte, readBlock blockReader) (*merkleTree, error) {
	if err := alg.validate(); err != nil {
		return nil, err
	}
	digestSize := alg.DigestSize()
	if len(salt) > SaltSize {
		return nil, fmt.Errorf("%w: length=%d", ErrInvalidSalt, len(salt))
	}

	if dataSize > ^uint64(0)-(BlockSize-1) {
		return nil, fmt.Errorf("%w: data size %d overflows", ErrSizeMismatch, dataSize)
	}
	blockCount64 := (dataSize + BlockSize - 1) / BlockSize
	maxInt := uint64(^uint(0) >> 1)
	if blockCount64 > maxInt {
		return nil, fmt.Errorf("%w: too many blocks", ErrSizeMismatch)
	}
	blockCount := int(blockCount64)
	levels := make([][][]byte, 0)
	root, err := alg.newHash()
	if err != nil {
		return nil, err
	}
	saltPrefix := make([]byte, alg.HashBlockSize())
	copy(saltPrefix, salt)

	if blockCount == 0 {
		levels = append(levels, [][]byte{})
		return &merkleTree{
			algorithm:     alg,
			saltPrefix:    saltPrefix,
			dataSize:      dataSize,
			numDataBlocks: 0,
			levels:        levels,
			root:          make([]byte, digestSize),
		}, nil
	}

	leaves := make([][]byte, blockCount)
	for i := 0; i < blockCount; i++ {
		block, err := readBlock(i)
		if err != nil {
			return nil, err
		}
		if len(block) > BlockSize {
			return nil, fmt.Errorf("%w: block %d has %d bytes", ErrCorruptBlock, i, len(block))
		}
		leaves[i] = hashBlock(root, saltPrefix, block, digestSize)
	}
	levels = append(levels, leaves)

	level := leaves
	for len(level) != 1 {
		next := hashLevel(root, saltPrefix, level, digestSize)
		levels = append(levels, next)
		level = next
	}
	return &merkleTree{
		algorithm:     alg,
		saltPrefix:    saltPrefix,
		dataSize:      dataSize,
		numDataBlocks: blockCount,
		levels:        levels,
		root:          append([]byte(nil), level[0]...),
	}, nil
}

func hashBlock(h hash.Hash, saltPrefix, block []byte, digestSize int) []byte {
	page := make([]byte, BlockSize)
	copy(page, block)
	h.Reset()
	_, _ = h.Write(saltPrefix)
	_, _ = h.Write(page)
	sum := h.Sum(nil)
	return append([]byte(nil), sum[:digestSize]...)
}

func hashLevel(h hash.Hash, saltPrefix []byte, digests [][]byte, digestSize int) [][]byte {
	perBlock := BlockSize / digestSize
	count := (len(digests) + perBlock - 1) / perBlock
	if count == 0 {
		count = 1
	}
	out := make([][]byte, count)
	for i := 0; i < count; i++ {
		page := make([]byte, BlockSize)
		start := i * perBlock
		end := start + perBlock
		if end > len(digests) {
			end = len(digests)
		}
		for j := start; j < end; j++ {
			off := (j - start) * digestSize
			copy(page[off:], digests[j])
		}
		h.Reset()
		_, _ = h.Write(saltPrefix)
		_, _ = h.Write(page)
		sum := h.Sum(nil)
		out[i] = append([]byte(nil), sum[:digestSize]...)
	}
	return out
}

func (t *merkleTree) verifyBlock(index int) ([]byte, error) {
	if index < 0 || index >= t.numDataBlocks {
		return nil, fmt.Errorf("%w: %d", ErrBlockIndex, index)
	}
	leaf := t.levels[0][index]
	current := index
	for level := 0; level < len(t.levels)-1; level++ {
		perBlock := BlockSize / t.algorithm.DigestSize()
		start := (current / perBlock) * perBlock
		end := start + perBlock
		if end > len(t.levels[level]) {
			end = len(t.levels[level])
		}
		page := make([]byte, BlockSize)
		for j := start; j < end; j++ {
			off := (j - start) * t.algorithm.DigestSize()
			copy(page[off:], t.levels[level][j])
		}
		h, err := t.algorithm.newHash()
		if err != nil {
			return nil, err
		}
		_, _ = h.Write(t.saltPrefix)
		_, _ = h.Write(page)
		got := h.Sum(nil)
		parentIndex := current / perBlock
		parent := t.levels[level+1][parentIndex]
		if subtle.ConstantTimeCompare(got[:len(parent)], parent) != 1 {
			return nil, ErrCorruptBlock
		}
		current = parentIndex
	}
	return leaf, nil
}

func (t *merkleTree) verify(d *Descriptor) error {
	if len(t.root) != len(d.RootHash) || subtle.ConstantTimeCompare(t.root, d.RootHash) != 1 {
		return ErrRootHashMismatch
	}
	return nil
}

func readAtFull(r io.ReaderAt, p []byte, off int64) error {
	for len(p) > 0 {
		n, err := r.ReadAt(p, off)
		if n > 0 {
			p = p[n:]
			off += int64(n)
		}
		if err != nil {
			if err == io.EOF && len(p) == 0 {
				return nil
			}
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}
