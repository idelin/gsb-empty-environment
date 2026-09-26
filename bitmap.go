// Package cowbitmap implements a fixed-length bitmap with block-level
// copy-on-write snapshots, lazily materialized binary operations, and
// reference-counted block reclamation. Standard library only.
//
// Storage layout in memory:
//
//	Bitmap
//	  nbits   int          logical bit length (fixed at construction)
//	  blocks  []*block     len = ceil(nbits / BlockBits); nil = all-zero block
//
//	block
//	  words   [64]uint64   512 bits = 64 bytes, little-endian bit order
//	  refs    int32        atomic refcount; shared by snapshots / lazy results
//
// Blocks are the unit of sharing: Snapshot() copies only the pointer slice
// and bumps each block's refcount. A write to a block with refs > 1 first
// clones that single block (copy-on-write), so old snapshots never observe
// new flips. All-zero blocks are stored as nil and cost 0 data bytes.
package cowbitmap

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"sync/atomic"
)

const (
	// BlockBits is the number of bits per storage block.
	BlockBits = 512
	// BlockWords is the number of uint64 words per block.
	BlockWords = BlockBits / 64
	// BlockBytes is the on-disk / in-memory data size of one block.
	BlockBytes = BlockBits / 8
)

var (
	ErrOutOfRange     = errors.New("cowbitmap: bit index out of range")
	ErrLengthMismatch = errors.New("cowbitmap: operand length mismatch")
	ErrReleased       = errors.New("cowbitmap: bitmap already released")
	ErrDoubleRelease  = errors.New("cowbitmap: release called twice")
	ErrBadEncoding    = errors.New("cowbitmap: invalid encoding")
)

// block is a reference-counted 512-bit storage chunk.
type block struct {
	words [BlockWords]uint64
	refs  int32 // accessed atomically
}

func (b *block) retain() { atomic.AddInt32(&b.refs, 1) }

func (b *block) release() {
	if atomic.AddInt32(&b.refs, -1) < 0 {
		panic("cowbitmap: block refcount went negative")
	}
}

func (b *block) isZero() bool {
	for _, w := range b.words {
		if w != 0 {
			return false
		}
	}
	return true
}

// Bitmap is a fixed-length, copy-on-write bit array.
type Bitmap struct {
	nbits    int
	blocks   []*block
	released bool
}

// New returns an all-zero bitmap of nbits bits.
func New(nbits int) *Bitmap {
	if nbits < 0 {
		panic("cowbitmap: negative length")
	}
	return &Bitmap{
		nbits:  nbits,
		blocks: make([]*block, (nbits+BlockBits-1)/BlockBits),
	}
}

// Len returns the logical bit length.
func (m *Bitmap) Len() int { return m.nbits }

// Snapshot returns a new Bitmap sharing all blocks with m. Writes to either
// bitmap copy only the affected block, so snapshots are fully isolated.
func (m *Bitmap) Snapshot() *Bitmap {
	s := &Bitmap{nbits: m.nbits, blocks: make([]*block, len(m.blocks))}
	for i, b := range m.blocks {
		if b != nil {
			b.retain()
		}
		s.blocks[i] = b
	}
	return s
}

// Set flips bit to v, copying the containing block first if it is shared.
func (m *Bitmap) Set(bit int, v bool) error {
	if m.released {
		return ErrReleased
	}
	if bit < 0 || bit >= m.nbits {
		return ErrOutOfRange
	}
	i := bit / BlockBits
	off := uint(bit % BlockBits)
	b := m.blocks[i]
	switch {
	case b == nil:
		if !v {
			return nil // clearing a zero block is a no-op
		}
		b = &block{refs: 1}
		m.blocks[i] = b
	case atomic.LoadInt32(&b.refs) > 1:
		nb := &block{words: b.words, refs: 1}
		b.release()
		m.blocks[i] = nb
		b = nb
	}
	mask := uint64(1) << (off % 64)
	if v {
		b.words[off/64] |= mask
	} else {
		b.words[off/64] &^= mask
		if b.isZero() { // reclaim: all-zero blocks are stored as nil
			m.blocks[i] = nil
			b.release()
		}
	}
	return nil
}

// Get reports whether bit is set.
func (m *Bitmap) Get(bit int) (bool, error) {
	if m.released {
		return false, ErrReleased
	}
	if bit < 0 || bit >= m.nbits {
		return false, ErrOutOfRange
	}
	b := m.blocks[bit/BlockBits]
	if b == nil {
		return false, nil
	}
	off := uint(bit % BlockBits)
	return b.words[off/64]&(uint64(1)<<(off%64)) != 0, nil
}

// Count returns the number of set bits.
func (m *Bitmap) Count() int {
	n := 0
	for _, b := range m.blocks {
		if b == nil {
			continue
		}
		for _, w := range b.words {
			n += bits.OnesCount64(w)
		}
	}
	return n
}

// retain bumps the refcount of every non-nil block (used by lazy operands).
func (m *Bitmap) retain() {
	for _, b := range m.blocks {
		if b != nil {
			b.retain()
		}
	}
}

// Release drops this bitmap's reference on every block. Blocks whose
// refcount reaches zero become garbage. A second Release returns
// ErrDoubleRelease and leaves refcounts untouched (never negative).
func (m *Bitmap) Release() error {
	if m.released {
		return ErrDoubleRelease
	}
	m.released = true
	for _, b := range m.blocks {
		if b != nil {
			b.release()
		}
	}
	m.blocks = nil
	return nil
}

// MemBytes estimates live heap bytes owned (possibly shared) by this bitmap:
// the block pointer slice plus each distinct non-nil block's 64 data bytes.
func (m *Bitmap) MemBytes() int {
	uniq := make(map[*block]struct{}, len(m.blocks))
	for _, b := range m.blocks {
		if b != nil {
			uniq[b] = struct{}{}
		}
	}
	return len(m.blocks)*8 + len(uniq)*BlockBytes
}

// SharedBlocks reports how many non-nil blocks have refs > 1.
func (m *Bitmap) SharedBlocks() int {
	n := 0
	for _, b := range m.blocks {
		if b != nil && atomic.LoadInt32(&b.refs) > 1 {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Lazy binary operations
// ---------------------------------------------------------------------------

type opKind uint8

const (
	opAnd opKind = iota
	opOr
)

// LazyBitmap is the unmaterialized result of And/Or. Blocks are computed on
// first access and cached; operand blocks are retained until Release.
type LazyBitmap struct {
	nbits    int
	op       opKind
	ab, bb   []*block // retained operand blocks (nil = zero block)
	cache    []*block // materialized results, nil = not yet computed / zero
	released bool
}

func binaryOp(a, b *Bitmap, op opKind) (*LazyBitmap, error) {
	// Length mismatch is rejected up front, before any retain or allocation.
	if a.nbits != b.nbits {
		return nil, ErrLengthMismatch
	}
	if a.released || b.released {
		return nil, ErrReleased
	}
	l := &LazyBitmap{
		nbits: a.nbits,
		op:    op,
		ab:    make([]*block, len(a.blocks)),
		bb:    make([]*block, len(b.blocks)),
		cache: make([]*block, len(a.blocks)),
	}
	for i, blk := range a.blocks {
		if blk != nil {
			blk.retain()
		}
		l.ab[i] = blk
	}
	for i, blk := range b.blocks {
		if blk != nil {
			blk.retain()
		}
		l.bb[i] = blk
	}
	return l, nil
}

// And returns the lazy conjunction of a and b.
func And(a, b *Bitmap) (*LazyBitmap, error) { return binaryOp(a, b, opAnd) }

// Or returns the lazy disjunction of a and b.
func Or(a, b *Bitmap) (*LazyBitmap, error) { return binaryOp(a, b, opOr) }

// Len returns the logical bit length of the result.
func (l *LazyBitmap) Len() int { return l.nbits }

// MaterializedBlocks reports how many result blocks have been computed so far.
func (l *LazyBitmap) MaterializedBlocks() int {
	n := 0
	for _, b := range l.cache {
		if b != nil {
			n++
		}
	}
	return n
}

// materialize computes (and caches) result block i, returning nil for an
// all-zero result block.
func (l *LazyBitmap) materialize(i int) *block {
	if b := l.cache[i]; b != nil {
		return b
	}
	ba, bb := l.ab[i], l.bb[i]
	var res *block
	switch l.op {
	case opAnd:
		if ba == nil || bb == nil {
			return nil // zero AND anything = zero
		}
		res = &block{refs: 1}
		for j := range res.words {
			res.words[j] = ba.words[j] & bb.words[j]
		}
	case opOr:
		if ba == nil { // share the operand block instead of copying
			if bb == nil {
				return nil
			}
			bb.retain()
			l.cache[i] = bb
			return bb
		}
		if bb == nil {
			ba.retain()
			l.cache[i] = ba
			return ba
		}
		res = &block{refs: 1}
		for j := range res.words {
			res.words[j] = ba.words[j] | bb.words[j]
		}
	}
	l.cache[i] = res
	return res
}

// Get reports whether bit is set, materializing only the containing block.
func (l *LazyBitmap) Get(bit int) (bool, error) {
	if l.released {
		return false, ErrReleased
	}
	if bit < 0 || bit >= l.nbits {
		return false, ErrOutOfRange
	}
	b := l.materialize(bit / BlockBits)
	if b == nil {
		return false, nil
	}
	off := uint(bit % BlockBits)
	return b.words[off/64]&(uint64(1)<<(off%64)) != 0, nil
}

// Materialize forces every block and returns the result as a plain Bitmap.
func (l *LazyBitmap) Materialize() (*Bitmap, error) {
	if l.released {
		return nil, ErrReleased
	}
	m := &Bitmap{nbits: l.nbits, blocks: make([]*block, len(l.cache))}
	for i := range m.blocks {
		if b := l.materialize(i); b != nil {
			b.retain()
			m.blocks[i] = b
		}
	}
	return m, nil
}

// Release drops operand and cached block references. A second call returns
// ErrDoubleRelease and leaves refcounts untouched (never negative).
func (l *LazyBitmap) Release() error {
	if l.released {
		return ErrDoubleRelease
	}
	l.released = true
	for _, b := range l.ab {
		if b != nil {
			b.release()
		}
	}
	for _, b := range l.bb {
		if b != nil {
			b.release()
		}
	}
	for _, b := range l.cache {
		if b != nil {
			b.release()
		}
	}
	l.ab, l.bb, l.cache = nil, nil, nil
	return nil
}

// ---------------------------------------------------------------------------
// Encoding
// ---------------------------------------------------------------------------
//
// Binary layout (all integers little-endian):
//
//	offset  size  field
//	0       4     magic "CWB1"
//	4       4     format version (1)
//	8       8     nbits    uint64
//	16      4     blockBits uint32 (must be 512)
//	20      4     nblocks  uint32
//	24      4     uniqBlocks uint32
//	28      64*U  unique block data, U blocks of BlockBytes each
//	28+64U  4*N   block table: per logical block, index into unique data,
//	              or 0xFFFFFFFF for an all-zero block
//
// Identical block contents are stored once, so snapshots that share blocks
// also share bytes on disk.

const (
	magic        = "CWB1"
	version      = 1
	headerBytes  = 28
	zeroBlockIdx = 0xFFFFFFFF
)

// EncodedLen returns the exact byte length of Encode() without encoding.
func (m *Bitmap) EncodedLen() int {
	uniq := make(map[[BlockWords]uint64]struct{}, len(m.blocks))
	for _, b := range m.blocks {
		if b != nil {
			uniq[b.words] = struct{}{}
		}
	}
	return headerBytes + len(uniq)*BlockBytes + len(m.blocks)*4
}

// Encode serializes the bitmap into the layout documented above.
func (m *Bitmap) Encode() []byte {
	uniqIdx := make(map[[BlockWords]uint64]uint32, len(m.blocks))
	var data []byte
	table := make([]byte, len(m.blocks)*4)
	for i, b := range m.blocks {
		if b == nil {
			binary.LittleEndian.PutUint32(table[i*4:], zeroBlockIdx)
			continue
		}
		idx, ok := uniqIdx[b.words]
		if !ok {
			idx = uint32(len(uniqIdx))
			uniqIdx[b.words] = idx
			for _, w := range b.words {
				var tmp [8]byte
				binary.LittleEndian.PutUint64(tmp[:], w)
				data = append(data, tmp[:]...)
			}
		}
		binary.LittleEndian.PutUint32(table[i*4:], idx)
	}
	buf := make([]byte, 0, headerBytes+len(data)+len(table))
	buf = append(buf, magic...)
	buf = binary.LittleEndian.AppendUint32(buf, version)
	buf = binary.LittleEndian.AppendUint64(buf, uint64(m.nbits))
	buf = binary.LittleEndian.AppendUint32(buf, BlockBits)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(m.blocks)))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(uniqIdx)))
	buf = append(buf, data...)
	buf = append(buf, table...)
	return buf
}

// Decode parses Encode output. Logically repeated blocks with identical
// content are reconstructed as a single shared block with a matching
// refcount, preserving the copy-on-write sharing structure.
func Decode(buf []byte) (*Bitmap, error) {
	if len(buf) < headerBytes || string(buf[:4]) != magic {
		return nil, ErrBadEncoding
	}
	if v := binary.LittleEndian.Uint32(buf[4:8]); v != version {
		return nil, ErrBadEncoding
	}
	nbits := binary.LittleEndian.Uint64(buf[8:16])
	if binary.LittleEndian.Uint32(buf[16:20]) != BlockBits {
		return nil, ErrBadEncoding
	}
	nblocks := binary.LittleEndian.Uint32(buf[20:24])
	uniq := binary.LittleEndian.Uint32(buf[24:28])
	if int(nblocks) != (int(nbits)+BlockBits-1)/BlockBits {
		return nil, ErrBadEncoding
	}
	if len(buf) != headerBytes+int(uniq)*BlockBytes+int(nblocks)*4 {
		return nil, ErrBadEncoding
	}
	data := buf[headerBytes : headerBytes+int(uniq)*BlockBytes]
	table := buf[headerBytes+int(uniq)*BlockBytes:]
	uniqBlocks := make([]*block, uniq)
	for i := range uniqBlocks {
		b := &block{}
		for j := 0; j < BlockWords; j++ {
			b.words[j] = binary.LittleEndian.Uint64(data[i*BlockBytes+j*8:])
		}
		uniqBlocks[i] = b
	}
	m := &Bitmap{nbits: int(nbits), blocks: make([]*block, nblocks)}
	for i := range m.blocks {
		idx := binary.LittleEndian.Uint32(table[i*4:])
		if idx == zeroBlockIdx {
			continue
		}
		if idx >= uniq {
			return nil, ErrBadEncoding
		}
		ub := uniqBlocks[idx]
		ub.refs++
		m.blocks[i] = ub
	}
	return m, nil
}
