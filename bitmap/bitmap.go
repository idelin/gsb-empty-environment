// Package bitmap implements a persistent bitmap with block-level
// copy-on-write, lazy binary operators and reference-counted reclamation.
//
// In-memory layout:
//
//	Bitmap  = header { refs, nbits, op, dir[], mat[], left, right }
//	dir     = block directory, one pointer per 4096-bit block (nil = all zero)
//	block   = [64]uint64 words = 512 bytes, shared between snapshots via COW
//
// A snapshot copies only the directory (8 bytes per slot) and bumps each
// block's reference count; a later flip copies exactly one 512-byte block.
package bitmap

import (
	"errors"
	"fmt"
	"math/bits"
	"sync/atomic"
	"unsafe"
)

const (
	// BlockBits is the number of bits per copy-on-write block.
	BlockBits = 4096
	// BlockWords is the number of uint64 words per block.
	BlockWords = BlockBits / 64
	// BlockBytes is the on-disk and in-memory size of one block.
	BlockBytes = BlockWords * 8 // 512
)

// ErrLengthMismatch is returned (wrapped) by And/Or when operand lengths differ.
var ErrLengthMismatch = errors.New("bitmap: operands have different lengths")

var liveBlocks atomic.Int64

// LiveBlocks reports how many 512-byte blocks are currently reachable.
// It is a global gauge used to verify reference reclamation.
func LiveBlocks() int64 { return liveBlocks.Load() }

type block struct {
	refs  int
	words [BlockWords]uint64
}

func newBlock() *block {
	liveBlocks.Add(1)
	return &block{refs: 1}
}

func (bl *block) release() {
	bl.refs--
	if bl.refs <= 0 {
		liveBlocks.Add(-1)
	}
}

type opKind uint8

const (
	opDense opKind = iota
	opAnd
	opOr
)

func (o opKind) String() string {
	switch o {
	case opAnd:
		return "AND"
	case opOr:
		return "OR"
	default:
		return "DENSE"
	}
}

// Bitmap is a persistent bitmap. It is not safe for concurrent use.
type Bitmap struct {
	refs  int
	nbits int
	op    opKind
	dir   []*block // dense: block directory; lazy: materialization cache
	mat   []bool   // lazy only: dir[i] has been computed
	left  *Bitmap  // lazy operands, retained while the node lives
	right *Bitmap
}

func numBlocks(nbits int) int {
	if nbits <= 0 {
		return 0
	}
	return (nbits + BlockBits - 1) / BlockBits
}

// New returns an empty dense bitmap of nbits bits with refcount 1.
func New(nbits int) *Bitmap {
	if nbits < 0 {
		panic("bitmap: negative length")
	}
	return &Bitmap{refs: 1, nbits: nbits, dir: make([]*block, numBlocks(nbits))}
}

// Len returns the number of bits addressable by the bitmap.
func (b *Bitmap) Len() int { return b.nbits }

// Op reports whether the node is DENSE, AND or OR.
func (b *Bitmap) Op() string { return b.op.String() }

// Refs returns the current reference count (for tests and diagnostics).
func (b *Bitmap) Refs() int { return b.refs }

// Retain increments the reference count and returns b.
func (b *Bitmap) Retain() *Bitmap {
	if b.refs < 1 {
		panic("bitmap: retain of released bitmap")
	}
	b.refs++
	return b
}

// Release decrements the reference count. When it reaches zero the node's
// block references and lazy operands are released recursively. Calling
// Release more times than Retain is a no-op: the count never goes negative.
func (b *Bitmap) Release() {
	if b.refs <= 0 {
		return
	}
	b.refs--
	if b.refs > 0 {
		return
	}
	for _, bl := range b.dir {
		if bl != nil {
			bl.release()
		}
	}
	b.dir = nil
	b.mat = nil
	if b.left != nil {
		b.left.Release()
		b.left = nil
	}
	if b.right != nil {
		b.right.Release()
		b.right = nil
	}
}

// Snapshot returns a new bitmap that shares every block with b. Subsequent
// writes to either copy only the affected 512-byte block, so the snapshot
// is immune to later flips of b (and vice versa).
func (b *Bitmap) Snapshot() *Bitmap {
	s := &Bitmap{refs: 1, nbits: b.nbits, op: b.op, left: b.left, right: b.right}
	if b.left != nil {
		b.left.Retain()
		b.right.Retain()
	}
	s.dir = make([]*block, len(b.dir))
	copy(s.dir, b.dir)
	for _, bl := range s.dir {
		if bl != nil {
			bl.refs++
		}
	}
	if b.mat != nil {
		s.mat = make([]bool, len(b.mat))
		copy(s.mat, b.mat)
	}
	return s
}

// blockAt returns the (possibly shared, read-only) block at directory index
// idx, materializing one block of a lazy node on demand.
func (b *Bitmap) blockAt(idx int) *block {
	if b.op == opDense || b.mat[idx] {
		return b.dir[idx]
	}
	l := b.left.blockAt(idx)
	r := b.right.blockAt(idx)
	var out *block
	switch b.op {
	case opAnd:
		if l != nil && r != nil {
			out = combine(l, r, func(x, y uint64) uint64 { return x & y })
		}
	case opOr:
		switch {
		case l == nil:
			out = r
		case r == nil:
			out = l
		default:
			out = combine(l, r, func(x, y uint64) uint64 { return x | y })
		}
	}
	if out != nil && out != l && out != r {
		// freshly combined block, owned by this node
	} else if out != nil {
		out.refs++ // shared with an operand; retain our reference
	}
	b.dir[idx] = out
	b.mat[idx] = true
	return out
}

func combine(l, r *block, f func(x, y uint64) uint64) *block {
	var out *block
	for i := 0; i < BlockWords; i++ {
		if v := f(l.words[i], r.words[i]); v != 0 {
			if out == nil {
				out = newBlock()
			}
			out.words[i] = v
		}
	}
	return out
}

// Set flips bit i to v with block-level copy-on-write: if the target block
// is shared with a snapshot it is first copied (512 bytes), so shared
// history is never mutated.
func (b *Bitmap) Set(i int, v bool) {
	if i < 0 || i >= b.nbits {
		panic("bitmap: index out of range")
	}
	idx := i / BlockBits
	bl := b.blockAt(idx)
	if bl == nil {
		if !v {
			return
		}
		bl = newBlock()
		b.dir[idx] = bl
		if b.op != opDense {
			b.mat[idx] = true
		}
	} else if bl.refs > 1 {
		nb := newBlock()
		copy(nb.words[:], bl.words[:])
		bl.release()
		b.dir[idx] = nb
		bl = nb
	}
	w := uint(i % BlockBits)
	if v {
		bl.words[w/64] |= 1 << (w % 64)
	} else {
		bl.words[w/64] &^= 1 << (w % 64)
	}
}

// Get reports bit i. Reading a lazy node materializes only the one block
// that contains i.
func (b *Bitmap) Get(i int) bool {
	if i < 0 || i >= b.nbits {
		return false
	}
	bl := b.blockAt(i / BlockBits)
	if bl == nil {
		return false
	}
	w := uint(i % BlockBits)
	return bl.words[w/64]&(1<<(w%64)) != 0
}

// Count returns the popcount of the bitmap. On a lazy node this forces
// full materialization.
func (b *Bitmap) Count() int {
	n := 0
	for i := range b.dir {
		if bl := b.blockAt(i); bl != nil {
			for _, w := range bl.words {
				n += bits.OnesCount64(w)
			}
		}
	}
	return n
}

// MaterializedBlocks reports how many directory slots currently hold a
// computed block. For a lazy node this starts at 0 and grows as blocks are
// touched; for a dense node it is the number of non-empty blocks.
func (b *Bitmap) MaterializedBlocks() int {
	n := 0
	if b.op == opDense {
		for _, bl := range b.dir {
			if bl != nil {
				n++
			}
		}
		return n
	}
	for _, m := range b.mat {
		if m {
			n++
		}
	}
	return n
}

// And returns a lazy conjunction. No block is computed until first access.
// Operand lengths must match or an error wrapping ErrLengthMismatch is
// returned before any node is created.
func And(a, b *Bitmap) (*Bitmap, error) { return lazyOp(opAnd, a, b) }

// Or returns a lazy disjunction. See And for the length contract.
func Or(a, b *Bitmap) (*Bitmap, error) { return lazyOp(opOr, a, b) }

func lazyOp(op opKind, a, b *Bitmap) (*Bitmap, error) {
	if a.nbits != b.nbits {
		return nil, fmt.Errorf("%w (%d vs %d bits)", ErrLengthMismatch, a.nbits, b.nbits)
	}
	a.Retain()
	b.Retain()
	n := numBlocks(a.nbits)
	return &Bitmap{
		refs:  1,
		nbits: a.nbits,
		op:    op,
		dir:   make([]*block, n),
		mat:   make([]bool, n),
		left:  a,
		right: b,
	}, nil
}

// MemStats breaks down the real in-memory footprint of the (sub)graph
// reachable from a bitmap, counting shared blocks and nodes only once.
type MemStats struct {
	Bitmaps   int   // live Bitmap headers
	Blocks    int   // live 512-byte blocks
	DirSlots  int   // block-directory slots (8 bytes each)
	MatSlots  int   // lazy materialization flags (1 byte each)
	TotalByte int64 // total bytes, header+directories+blocks
}

// headerBytes is the real in-memory size of one Bitmap header, measured
// from the struct itself rather than assumed.
var headerBytes = int64(unsafe.Sizeof(Bitmap{}))

// MemStats computes the byte-accurate footprint of b's reachable graph.
func (b *Bitmap) MemStats() MemStats {
	seenNode := map[*Bitmap]struct{}{}
	seenBlock := map[*block]struct{}{}
	var st MemStats
	var walk func(x *Bitmap)
	walk = func(x *Bitmap) {
		if x == nil {
			return
		}
		if _, ok := seenNode[x]; ok {
			return
		}
		seenNode[x] = struct{}{}
		st.Bitmaps++
		st.DirSlots += len(x.dir)
		st.MatSlots += len(x.mat)
		for _, bl := range x.dir {
			if bl == nil {
				continue
			}
			if _, ok := seenBlock[bl]; ok {
				continue
			}
			seenBlock[bl] = struct{}{}
			st.Blocks++
		}
		walk(x.left)
		walk(x.right)
	}
	walk(b)
	st.TotalByte = int64(st.Bitmaps)*headerBytes +
		int64(st.DirSlots)*8 +
		int64(st.MatSlots) +
		int64(st.Blocks)*BlockBytes
	return st
}
