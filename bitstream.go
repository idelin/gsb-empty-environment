// Package bitstream implements arbitrary-width (up to 64-bit) unaligned
// bit reading and writing with independently selectable bit order and
// byte order, nested read transactions (peek / mark / rollback), a
// shared input retention water mark, zero-copy cross-chunk views, and
// bounded decode quotas.
//
// The on-disk framing is self-describing: magic, format version, order
// bytes, payload bit length and a CRC-32 (IEEE) checksum. No third-party
// (de)compression library is used; framing fields have explicit lengths.
package bitstream

import (
	"errors"
	"hash/crc32"
	"sync"
)

// FormatVersion is the wire format produced by Marshal and consumed by
// Unmarshal.
const FormatVersion uint8 = 1

// BitOrder defines the order of bits inside one byte.
type BitOrder uint8

const (
	// MSBFirst numbers bits inside a byte 7..0 (bit 7 of the first byte
	// is the first bit of the stream).
	MSBFirst BitOrder = iota
	// LSBFirst numbers bits inside a byte 0..7 (bit 0 of the first byte
	// is the first bit of the stream).
	LSBFirst
)

// ByteOrder defines the order of whole bytes for multi-byte values.
type ByteOrder uint8

const (
	// BigEndian stores the most significant byte first.
	BigEndian ByteOrder = iota
	// LittleEndian stores the least significant byte first.
	LittleEndian
)

// Config fully describes a stream layout. BitOrder and ByteOrder are
// orthogonal: bit order maps logical bit positions onto bits within a
// byte, byte order maps logical byte positions onto storage bytes.
type Config struct {
	Bits  BitOrder
	Bytes ByteOrder
}

// Errors returned by the package. They intentionally do not implement
// Timeout so callers can distinguish structural failures from limits.
var (
	// ErrInvalidWidth is returned when a read or write width is outside
	// [1,64]. It is rejected before any buffer state changes.
	ErrInvalidWidth = errors.New("bitstream: width must be in [1,64]")
	// ErrShortBuffer reports that fewer bits were available than
	// requested. A failed read never consumes any bit.
	ErrShortBuffer = errors.New("bitstream: not enough bits available")
	// ErrQuotaExceeded reports that an operation would exceed the
	// reader's output quota. It consumes no input.
	ErrQuotaExceeded = errors.New("bitstream: decoder output quota exceeded")
	// ErrReleased reports access through a cursor whose backing chunk has
	// already been reclaimed.
	ErrReleased = errors.New("bitstream: backing block already released")
	// ErrRollbackReleased reports a rollback to a mark whose retained
	// prefix has been reclaimed.
	ErrRollbackReleased = errors.New("bitstream: rollback target block released")
	// ErrBadMagic / ErrBadVersion / ErrBadChecksum describe framing failures.
	ErrBadMagic    = errors.New("bitstream: bad magic")
	ErrBadVersion  = errors.New("bitstream: unsupported format version")
	ErrBadChecksum = errors.New("bitstream: crc32 checksum mismatch")
	ErrTruncated   = errors.New("bitstream: truncated frame")
)

// maxWidth is the largest number of bits transferable in one operation.
const maxWidth = 64

// block is one immutable physical chunk of the bit stream. Blocks are
// linked into a singly linked list; append never copies prior blocks and
// a view can span block boundaries without copying.
type block struct {
	data     []byte
	bits     int // number of valid bits in data (last block may be partial)
	start    int // global bit offset of bit zero of this block
	refs     int // live readers/marks whose position lies in/after block
	next     *block
	released bool // backing byte slice handed back / invalidated
}

// pool tracks physical buffer occupancy and reclamation. It is safe for
// concurrent use; the Reader API itself is not.
type pool struct {
	mu        sync.Mutex
	blocks    map[*block]struct{}
	allocated int // bytes currently held by live blocks
	reclaimed int // bytes returned over the lifetime
}

var blockPool = &pool{blocks: make(map[*block]struct{})}

func (p *pool) add(b *block, nBytes int) {
	p.mu.Lock()
	p.blocks[b] = struct{}{}
	p.allocated += nBytes
	p.mu.Unlock()
}

func (p *pool) reclaim(b *block) {
	p.mu.Lock()
	if _, ok := p.blocks[b]; ok {
		delete(p.blocks, b)
		p.allocated += -len(b.data)
		p.reclaimed += len(b.data)
	}
	p.mu.Unlock()
}

// Stats reports physical buffer occupancy and lifetime reclamation.
type Stats struct {
	LiveBlocks int
	LiveBytes  int
	Reclaimed  int
}

// BufferStats returns the current physical buffer accounting for blocks
// created by this package's readers/writers.
func BufferStats() Stats {
	blockPool.mu.Lock()
	defer blockPool.mu.Unlock()
	return Stats{
		LiveBlocks: len(blockPool.blocks),
		LiveBytes:  blockPool.allocated,
		Reclaimed:  blockPool.reclaimed,
	}
}

// Writer appends arbitrary-width values to a bit stream. Bytes are laid
// out according to cfg.Bytes; within every byte bits are numbered by
// cfg.Bits.
type Writer struct {
	cfg Config
	buf []byte // current accumulation block
	pos int    // bits already written
}

// NewWriter returns an empty writer with the given layout.
func NewWriter(cfg Config) *Writer {
	return &Writer{cfg: cfg}
}

// Len returns the number of bits written so far.
func (w *Writer) Len() int { return w.pos }

// Write writes the low nBits bits of v. Widths outside [1,64] are
// rejected before any state changes.
func (w *Writer) Write(v uint64, nBits int) error {
	if nBits < 1 || nBits > maxWidth {
		return ErrInvalidWidth
	}
	v &= ^uint64(0) >> (maxWidth - nBits)
	needBytes := (w.pos + nBits + 7) / 8
	if needBytes > len(w.buf) {
		grown := make([]byte, needBytes*2+8)
		copy(grown, w.buf)
		w.buf = grown
	}
	w.put(v, nBits)
	w.pos += nBits
	return nil
}

// put stores nBits bits of v starting at bit w.pos, honoring cfg.
func (w *Writer) put(v uint64, nBits int) {
	// Emit logical bytes, then place each byte's bits independently.
	nBytes := (nBits + 7) / 8
	for i := 0; i < nBytes; i++ {
		lo := i * 8
		width := nBits - lo
		if width > 8 {
			width = 8
		}
		var logicalByte uint8
		if w.cfg.Bytes == LittleEndian {
			logicalByte = uint8(v >> lo)
		} else {
			logicalByte = uint8(v >> (nBits - lo - width))
		}
		base := w.pos + lo
		for k := 0; k < width; k++ {
			// The k-th transmitted bit of this field: for MSBFirst the
			// field's most significant bit goes first, for LSBFirst its
			// least significant bit.
			src := uint(width - 1 - k)
			if w.cfg.Bits == LSBFirst {
				src = uint(k)
			}
			if logicalByte&(1<<src) != 0 {
				w.setBit(base + k)
			}
		}
	}
}

func (w *Writer) setBit(global int) {
	byteIdx, bitIdx := global/8, uint(global%8)
	if w.cfg.Bits == LSBFirst {
		w.buf[byteIdx] |= 1 << bitIdx
	} else {
		w.buf[byteIdx] |= 1 << (7 - bitIdx)
	}
}

// Bytes returns the packed stream (length ceil(Len/8)).
func (w *Writer) Bytes() []byte {
	out := make([]byte, (w.pos+7)/8)
	copy(out, w.buf)
	return out
}

// Chain is a zero-copy append-only sequence of blocks. Readers attach to
// a chain; views and marks pin the blocks they span so the shared
// retention water mark never reclaims memory still reachable by a
// rollback or an open view.
type Chain struct {
	head, tail *block
	length     int // total bits
}

// AppendBytes appends b as one block without copying. nBits may be
// smaller than len(b)*8 to describe a partial final byte.
func (c *Chain) AppendBytes(b []byte, nBits int) {
	if nBits < 0 || nBits > len(b)*8 {
		panic("bitstream: AppendBytes nBits out of range")
	}
	blk := &block{data: b, bits: nBits, start: c.length}
	blockPool.add(blk, len(b))
	if c.tail != nil {
		c.tail.next = blk
	} else {
		c.head = blk
	}
	c.tail = blk
	c.length += nBits
}

// Len returns the total number of bits in the chain.
func (c *Chain) Len() int { return c.length }

// locate returns the block containing global bit position p.
func (c *Chain) locate(p int) *block {
	for b := c.head; b != nil; b = b.next {
		if p < b.start+b.bits {
			return b
		}
	}
	return nil
}

// releaseBefore reclaims every unpinned block ending at or before the
// global bit position wm (the retention water mark). Pinned blocks
// (open views, active marks) are skipped; released blocks are detached
// and any later access through them fails with ErrReleased.
func (c *Chain) releaseBefore(wm int) {
	for c.head != nil && c.head.start+c.head.bits <= wm && c.head.refs == 0 {
		b := c.head
		c.head = b.next
		if c.tail == b {
			c.tail = nil
		}
		blockPool.reclaim(b)
		b.released = true
		b.data = nil
	}
}

// Reader consumes bits from a Chain. It supports peeking, nested
// mark/rollback transactions, bounded output quotas and cancellation.
// All of peek, mark and rollback share the chain's retention water
// mark: memory at or after the oldest active mark is never reclaimed.
type Reader struct {
	cfg   Config
	chain *Chain
	pos   int   // global bit cursor
	marks []int // nested transaction stack (global bit positions)
	quota int64 // remaining output bits; negative means unlimited
}

// NewReader attaches a reader to c at bit position 0 with no quota.
func NewReader(cfg Config, c *Chain) *Reader {
	return &Reader{cfg: cfg, chain: c, quota: -1}
}

// SetQuota bounds the total number of bits Read may still produce.
// Peeks and rollbacks never spend quota. A negative value lifts the
// limit.
func (r *Reader) SetQuota(bits int64) { r.quota = bits }

// Pos returns the current global bit cursor.
func (r *Reader) Pos() int { return r.pos }

// Remaining returns the number of unread bits in the chain.
func (r *Reader) Remaining() int { return r.chain.length - r.pos }

// bitAt returns the logical bit value at global stream position p.
func (r *Reader) bitAt(p int) (uint8, error) {
	b := r.chain.locate(p)
	if b == nil {
		return 0, ErrShortBuffer
	}
	if b.released {
		return 0, ErrReleased
	}
	local := p - b.start
	byteIdx, bitIdx := local/8, uint(local%8)
	if r.cfg.Bits == LSBFirst {
		return (b.data[byteIdx] >> bitIdx) & 1, nil
	}
	return (b.data[byteIdx] >> (7 - bitIdx)) & 1, nil
}

// gather assembles nBits bits starting at global position start without
// moving any cursor. It is the shared core of Read and Peek.
func (r *Reader) gather(start, nBits int) (uint64, error) {
	if nBits < 1 || nBits > maxWidth {
		return 0, ErrInvalidWidth
	}
	if r.chain.length-start < nBits {
		return 0, ErrShortBuffer
	}
	var v uint64
	nBytes := (nBits + 7) / 8
	for i := 0; i < nBytes; i++ {
		lo := i * 8
		width := nBits - lo
		if width > 8 {
			width = 8
		}
		var logicalByte uint8
		for k := 0; k < width; k++ {
			bit, err := r.bitAt(start + lo + k)
			if err != nil {
				return 0, err
			}
			if r.cfg.Bits == LSBFirst {
				logicalByte |= bit << uint(k)
			} else {
				logicalByte |= bit << uint(width-1-k)
			}
		}
		if r.cfg.Bytes == LittleEndian {
			v |= uint64(logicalByte) << uint(lo)
		} else {
			v |= uint64(logicalByte) << uint(nBits-lo-width)
		}
	}
	return v, nil
}

// checkQuota verifies the operation may produce nBits of output.
func (r *Reader) checkQuota(nBits int) error {
	if r.quota >= 0 && int64(nBits) > r.quota {
		return ErrQuotaExceeded
	}
	return nil
}

// Read consumes and returns the next nBits bits. Width errors are
// rejected before any state changes; short buffers, quota failures and
// cancellation consume no input.
func (r *Reader) Read(nBits int) (uint64, error) {
	return r.ReadCtx(nil, nBits)
}

// ReadCtx is Read with cancellation. A cancelled read consumes nothing.
func (r *Reader) ReadCtx(done <-chan struct{}, nBits int) (uint64, error) {
	if nBits < 1 || nBits > maxWidth {
		return 0, ErrInvalidWidth
	}
	if done != nil {
		select {
		case <-done:
			return 0, ErrCanceled
		default:
		}
	}
	if err := r.checkQuota(nBits); err != nil {
		return 0, err
	}
	v, err := r.gather(r.pos, nBits)
	if err != nil {
		return 0, err
	}
	r.pos += nBits
	if r.quota >= 0 {
		r.quota -= int64(nBits)
	}
	r.compact()
	return v, nil
}

// ErrCanceled reports a read cancelled through its done channel.
var ErrCanceled = errors.New("bitstream: read canceled")

// Peek returns the next nBits bits without moving the cursor and
// without spending quota.
func (r *Reader) Peek(nBits int) (uint64, error) {
	if nBits < 1 || nBits > maxWidth {
		return 0, ErrInvalidWidth
	}
	return r.gather(r.pos, nBits)
}

// Align advances the cursor to the next multiple of nBits bits
// (typically 8 for byte alignment). It never moves backwards.
func (r *Reader) Align(nBits int) {
	if nBits <= 0 {
		return
	}
	if rem := r.pos % nBits; rem != 0 {
		r.pos += nBits - rem
	}
	r.compact()
}

// Mark opens a nested transaction at the current position. Marks nest;
// Rollback returns to the most recent uncommitted mark.
func (r *Reader) Mark() {
	r.marks = append(r.marks, r.pos)
	if b := r.chain.locate(r.pos); b != nil {
		b.refs++
	}
}

// Commit closes the innermost transaction, keeping the current cursor.
func (r *Reader) Commit() error {
	if len(r.marks) == 0 {
		return ErrNoMark
	}
	m := r.marks[len(r.marks)-1]
	r.marks = r.marks[:len(r.marks)-1]
	if b := r.chain.locate(m); b != nil {
		b.refs--
	}
	r.compact()
	return nil
}

// ErrNoMark reports Commit/Rollback without an open transaction.
var ErrNoMark = errors.New("bitstream: no open mark")

// Rollback returns the cursor to the innermost mark and closes that
// transaction. Rolling back into a released block fails and leaves the
// cursor untouched.
func (r *Reader) Rollback() error {
	if len(r.marks) == 0 {
		return ErrNoMark
	}
	m := r.marks[len(r.marks)-1]
	b := r.chain.locate(m)
	if b != nil && b.released {
		return ErrRollbackReleased
	}
	r.marks = r.marks[:len(r.marks)-1]
	if b != nil {
		b.refs--
	}
	r.pos = m
	return nil
}

// watermark is the oldest bit position still reachable by rollback, or
// the cursor when no transaction is open. Peek, mark and rollback all
// share this single retention water mark.
func (r *Reader) watermark() int {
	wm := r.pos
	if len(r.marks) > 0 && r.marks[0] < wm {
		wm = r.marks[0]
	}
	return wm
}

// compact reclaims unpinned blocks below the retention water mark.
func (r *Reader) compact() { r.chain.releaseBefore(r.watermark()) }

// View is a zero-copy window over [start, start+bits) of a chain. It
// pins every block it spans until Close, so views may cross block
// boundaries without copying.
type View struct {
	chain *Chain
	start int
	bits  int
	blks  []*block
	done  bool
}

// View returns a zero-copy view of the next nBits bits and advances the
// cursor past them. The view keeps its blocks alive until Close.
func (r *Reader) View(nBits int) (*View, error) {
	if nBits < 0 {
		return nil, ErrInvalidWidth
	}
	if r.chain.length-r.pos < nBits {
		return nil, ErrShortBuffer
	}
	v := &View{chain: r.chain, start: r.pos, bits: nBits}
	if nBits > 0 {
		for b := r.chain.locate(r.pos); b != nil && b.start < r.pos+nBits; b = b.next {
			if b.released {
				return nil, ErrReleased
			}
			b.refs++
			v.blks = append(v.blks, b)
		}
	}
	r.pos += nBits
	r.compact()
	return v, nil
}

// Reader returns a reader confined to the view's window, sharing the
// underlying blocks (zero-copy).
func (v *View) Reader(cfg Config) *Reader {
	sub := &Chain{length: v.start + v.bits}
	// Relink the pinned blocks into the sub-chain without copying.
	if len(v.blks) > 0 {
		sub.head = v.blks[0]
		sub.tail = v.blks[len(v.blks)-1]
	}
	r := NewReader(cfg, sub)
	r.pos = v.start
	return r
}

// Close releases the view's pins. Using the view afterwards fails.
func (v *View) Close() {
	if v.done {
		return
	}
	v.done = true
	for _, b := range v.blks {
		b.refs--
	}
	v.blks = nil
}

// ---------------------------------------------------------------------------
// Self-describing wire frame.
//
// Layout (all integers big-endian, lengths explicit):
//
//	0..3   magic "BTS1"
//	4      format version (FormatVersion)
//	5      bit order (0=MSBFirst, 1=LSBFirst)
//	6      byte order (0=BigEndian, 1=LittleEndian)
//	7      reserved, must be 0
//	8..11  payload length in bits (uint32)
//	12..   payload bytes, ceil(bits/8)
//	last 4 crc32-IEEE of bytes 0..len-5
// ---------------------------------------------------------------------------

const (
	frameMagic   = "BTS1"
	frameHeader  = 12
	frameTrailer = 4
)

// Marshal packs payload (bits long) into a self-describing frame.
func Marshal(cfg Config, payload []byte, bits int) ([]byte, error) {
	if bits < 0 || bits > len(payload)*8 {
		return nil, ErrInvalidWidth
	}
	n := frameHeader + (bits+7)/8 + frameTrailer
	out := make([]byte, n)
	copy(out[0:4], frameMagic)
	out[4] = FormatVersion
	out[5] = byte(cfg.Bits)
	out[6] = byte(cfg.Bytes)
	out[7] = 0
	be32(out[8:12], uint32(bits))
	copy(out[frameHeader:], payload)
	crc := crc32.ChecksumIEEE(out[:n-frameTrailer])
	be32(out[n-frameTrailer:], crc)
	return out, nil
}

// Unmarshal validates a frame and returns its config, payload and bit
// length. The payload aliases the input (zero-copy); maxBits bounds the
// declared payload so a corrupt length cannot cause an oversized
// allocation downstream.
func Unmarshal(frame []byte, maxBits uint32) (Config, []byte, int, error) {
	var cfg Config
	if len(frame) < frameHeader+frameTrailer {
		return cfg, nil, 0, ErrTruncated
	}
	if string(frame[0:4]) != frameMagic {
		return cfg, nil, 0, ErrBadMagic
	}
	if frame[4] != FormatVersion {
		return cfg, nil, 0, ErrBadVersion
	}
	n := len(frame)
	want := crc32.ChecksumIEEE(frame[:n-frameTrailer])
	if got := rd32(frame[n-frameTrailer:]); got != want {
		return cfg, nil, 0, ErrBadChecksum
	}
	cfg.Bits = BitOrder(frame[5])
	cfg.Bytes = ByteOrder(frame[6])
	if cfg.Bits != MSBFirst && cfg.Bits != LSBFirst {
		return cfg, nil, 0, ErrBadVersion
	}
	if cfg.Bytes != BigEndian && cfg.Bytes != LittleEndian {
		return cfg, nil, 0, ErrBadVersion
	}
	bits := rd32(frame[8:12])
	if bits > maxBits {
		return cfg, nil, 0, ErrQuotaExceeded
	}
	need := frameHeader + int((bits+7)/8) + frameTrailer
	if len(frame) < need {
		return cfg, nil, 0, ErrTruncated
	}
	return cfg, frame[frameHeader : frameHeader+int((bits+7)/8)], int(bits), nil
}

func be32(dst []byte, v uint32) {
	dst[0] = byte(v >> 24)
	dst[1] = byte(v >> 16)
	dst[2] = byte(v >> 8)
	dst[3] = byte(v)
}

func rd32(src []byte) uint32 {
	return uint32(src[0])<<24 | uint32(src[1])<<16 | uint32(src[2])<<8 | uint32(src[3])
}
