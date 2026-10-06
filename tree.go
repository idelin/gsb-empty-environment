// Package integrity implements a binary Merkle digest tree over a fixed
// number of blocks. Missing blocks are represented by explicit empty
// leaves, the last level is padded to a power of two, and every proof
// carries the block index and total block count so a leaf from another
// position (or another snapshot size) cannot masquerade as the block
// being proven.
package integrity

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
)

// Domain separation tags for the hash inputs.
const (
	tagLeafData  byte = 0x00 // leaf holding a real block
	tagNode      byte = 0x01 // interior node
	tagLeafEmpty byte = 0x02 // explicit empty leaf (missing block / padding)
)

// HashSize is the size in bytes of every digest in the tree.
const HashSize = sha256.Size

// ErrIndexOutOfRange is returned when a block index is >= the block count.
var ErrIndexOutOfRange = errors.New("integrity: block index out of range")

// ErrEmptyProof is returned when a batch proof is requested for no blocks.
var ErrEmptyProof = errors.New("integrity: batch proof needs at least one index")

// HashBlock returns the digest of a single block's contents. A block of
// all-zero bytes still produces a real digest; only a nil block hash marks
// a missing block.
func HashBlock(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

// leafHash binds the block digest to its index and the total block count.
func leafHash(index, total uint64, blockHash []byte) []byte {
	h := sha256.New()
	h.Write([]byte{tagLeafData})
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:8], index)
	binary.BigEndian.PutUint64(buf[8:16], total)
	h.Write(buf[:])
	h.Write(blockHash)
	return h.Sum(nil)
}

// emptyLeafHash is the explicit empty leaf for a missing block or padding.
// It is position- and count-bound and cannot collide with any real block
// leaf because of the distinct domain tag.
func emptyLeafHash(index, total uint64) []byte {
	h := sha256.New()
	h.Write([]byte{tagLeafEmpty})
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:8], index)
	binary.BigEndian.PutUint64(buf[8:16], total)
	h.Write(buf[:])
	return h.Sum(nil)
}

func nodeHash(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{tagNode})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// paddedSize returns the smallest power of two >= n, at least 1.
func paddedSize(n uint64) uint64 {
	p := uint64(1)
	for p < n {
		p <<= 1
	}
	return p
}

// depthFor returns the number of edges from leaf to root for total blocks.
func depthFor(total uint64) int {
	depth := 0
	for p := paddedSize(total); p > 1; p >>= 1 {
		depth++
	}
	return depth
}

func clone(b []byte) []byte {
	if b == nil {
		return nil
	}
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// Tree is a binary Merkle digest tree over a fixed number of blocks.
// A nil block hash denotes a missing block, stored as an explicit empty
// leaf. The tree is safe for concurrent use: every proof is generated
// against one consistent snapshot.
type Tree struct {
	mu     sync.RWMutex
	total  uint64
	blocks [][]byte   // len == total; nil entry = missing block
	layers [][][]byte // layers[0] = padded leaves, last layer = root
}

func buildLayers(blocks [][]byte) [][][]byte {
	total := uint64(len(blocks))
	size := paddedSize(total)
	leaves := make([][]byte, size)
	for i := uint64(0); i < size; i++ {
		if i < total && blocks[i] != nil {
			leaves[i] = leafHash(i, total, blocks[i])
		} else {
			leaves[i] = emptyLeafHash(i, total)
		}
	}
	layers := [][][]byte{leaves}
	for len(layers[len(layers)-1]) > 1 {
		cur := layers[len(layers)-1]
		next := make([][]byte, len(cur)/2)
		for i := range next {
			next[i] = nodeHash(cur[2*i], cur[2*i+1])
		}
		layers = append(layers, next)
	}
	return layers
}

// New builds a tree over the given block hashes. A nil entry marks a
// missing block. The slice is copied.
func New(blocks [][]byte) *Tree {
	copied := make([][]byte, len(blocks))
	for i, b := range blocks {
		copied[i] = clone(b)
	}
	t := &Tree{total: uint64(len(copied)), blocks: copied}
	t.layers = buildLayers(copied)
	return t
}

// root must be called with the lock held.
func (t *Tree) root() []byte {
	return t.layers[len(t.layers)-1][0]
}

// Root returns the current tree root.
func (t *Tree) Root() []byte {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return clone(t.root())
}

// Total returns the number of blocks in the snapshot.
func (t *Tree) Total() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.total
}

// BlockHash returns the hash of block i and whether the block is present.
func (t *Tree) BlockHash(i uint64) ([]byte, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if i >= t.total {
		return nil, false
	}
	return clone(t.blocks[i]), t.blocks[i] != nil
}

// Resize changes the block count, keeping existing blocks where possible.
// Growth extends the file with missing blocks; truncation drops the tail.
// The tree is rebuilt in full.
func (t *Tree) Resize(newTotal uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	blocks := make([][]byte, newTotal)
	copy(blocks, t.blocks)
	t.blocks = blocks
	t.total = newTotal
	t.layers = buildLayers(blocks)
}

// siblingsAt returns the sibling hashes along the path from leaf index to
// the root, leaf level first. The lock must be held.
func (t *Tree) siblingsAt(index uint64) [][]byte {
	depth := len(t.layers) - 1
	siblings := make([][]byte, depth)
	pos := index
	for level := 0; level < depth; level++ {
		siblings[level] = clone(t.layers[level][pos^1])
		pos >>= 1
	}
	return siblings
}

// Update replaces the block at index (nil blockHash marks it missing) and
// returns a proof binding the old root, the new root and the block count.
// Only the path from the updated leaf to the root is recomputed.
func (t *Tree) Update(index uint64, blockHash []byte) (UpdateProof, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if index >= t.total {
		return UpdateProof{}, ErrIndexOutOfRange
	}
	proof := UpdateProof{
		Index:       index,
		Total:       t.total,
		OldBlock:    clone(t.blocks[index]),
		OldSiblings: t.siblingsAt(index),
		OldRoot:     clone(t.root()),
	}

	t.blocks[index] = clone(blockHash)
	if blockHash == nil {
		t.layers[0][index] = emptyLeafHash(index, t.total)
	} else {
		t.layers[0][index] = leafHash(index, t.total, blockHash)
	}
	pos := index
	for level := 0; level+1 < len(t.layers); level++ {
		parent := pos >> 1
		t.layers[level+1][parent] = nodeHash(t.layers[level][pos&^1], t.layers[level][pos&^1+1])
		pos = parent
	}

	proof.NewBlock = clone(blockHash)
	proof.NewSiblings = t.siblingsAt(index)
	proof.NewRoot = clone(t.root())
	return proof, nil
}
