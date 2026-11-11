// Package isoinput provides input isolation for security differential
// testing: a single mutable input is lent to multiple parsers through
// read-only spans, so one side cannot mutate the bytes another side is
// parsing and manufacture a fake divergence. Parsers that need to
// modify data must take an independent copy. The source records the
// digest of the initial input and each side records the ranges it
// actually read, so both sides' evidence can be tied back to the same
// initial input. Blocks are reference counted per borrower; cancelling
// one side never reclaims blocks still borrowed by the other.
package isoinput

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Range is a half-open byte range [Off, Off+Len) in the logical input.
type Range struct {
	Off int64
	Len int64
}

// Evidence ties one side's parse run to the initial input: the digest
// of the whole input as first seen, plus the ranges the side read.
type Evidence struct {
	Side   string
	Digest [sha256.Size]byte
	Ranges []Range
}

type block struct {
	data []byte
	off  int64 // offset of data[0] in the logical input
	refs atomic.Int32
	// reclaimed reports whether the block's memory was returned to the
	// allocator. Only happens when refs drops to zero.
	reclaimed atomic.Bool
}

// Source owns the initial input, split into one or more blocks, and
// hands out per-side borrow handles. It is safe for concurrent use.
type Source struct {
	blocks []*block
	size   int64
	digest [sha256.Size]byte
	copied atomic.Int64 // bytes handed out as independent copies
}

// NewSource takes ownership of blocks (callers must not mutate them
// afterwards) and computes the digest of the logical concatenation.
func NewSource(blocks ...[]byte) *Source {
	s := &Source{}
	h := sha256.New()
	var off int64
	for _, b := range blocks {
		if len(b) == 0 {
			continue
		}
		blk := &block{data: b, off: off}
		s.blocks = append(s.blocks, blk)
		h.Write(b)
		off += int64(len(b))
	}
	s.size = off
	copy(s.digest[:], h.Sum(nil))
	return s
}

// Size is the total length of the logical input.
func (s *Source) Size() int64 { return s.size }

// Digest is the SHA-256 of the initial input, captured at construction.
func (s *Source) Digest() [sha256.Size]byte { return s.digest }

// CopiedBytes reports how many bytes were handed out as independent
// mutable copies. Zero-copy parsing keeps this at zero.
func (s *Source) CopiedBytes() int64 { return s.copied.Load() }

// VerifyIntegrity recomputes the digest over the live blocks and
// reports whether the shared input is still byte-identical to the
// initial input. A false result means someone mutated shared memory.
func (s *Source) VerifyIntegrity() bool {
	h := sha256.New()
	for _, b := range s.blocks {
		h.Write(b.data)
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum == s.digest
}

// Side returns a borrow handle for one parser.
func (s *Source) Side(name string) *Side {
	return &Side{src: s, name: name}
}

// BlockReclaimed reports whether block i's memory has been returned to
// the allocator (i.e. no side borrows it anymore).
func (s *Source) BlockReclaimed(i int) bool { return s.blocks[i].reclaimed.Load() }

// NumBlocks is the number of underlying storage blocks.
func (s *Source) NumBlocks() int { return len(s.blocks) }

var (
	ErrOutOfRange = errors.New("isoinput: range out of bounds")
	ErrCancelled  = errors.New("isoinput: side is cancelled")
)

// Side is one parser's borrow handle. All spans borrowed through it are
// tracked, and Cancel releases exactly this side's references.
type Side struct {
	src  *Source
	name string

	mu        sync.Mutex
	spans     map[*Span]struct{}
	reads     []Range
	cancelled bool
}

// Borrow returns a zero-copy read-only span over [off, off+n). The span
// may cross block boundaries. The caller must Release it when done.
func (sd *Side) Borrow(off, n int64) (*Span, error) {
	if off < 0 || n < 0 || off+n > sd.src.size {
		return nil, fmt.Errorf("%w: [%d,%d) of %d", ErrOutOfRange, off, off+n, sd.src.size)
	}
	sd.mu.Lock()
	defer sd.mu.Unlock()
	if sd.cancelled {
		return nil, ErrCancelled
	}
	sp := &Span{side: sd, off: off, length: n}
	remaining := n
	pos := off
	for _, b := range sd.src.blocks {
		if remaining == 0 {
			break
		}
		bEnd := b.off + int64(len(b.data))
		if pos >= bEnd || pos+remaining <= b.off {
			continue
		}
		start := pos - b.off
		take := int64(len(b.data)) - start
		if take > remaining {
			take = remaining
		}
		b.refs.Add(1)
		sp.segs = append(sp.segs, segment{blk: b, view: b.data[start : start+take]})
		pos += take
		remaining -= take
	}
	sd.trackLocked(sp)
	return sp, nil
}

func (sd *Side) trackLocked(sp *Span) {
	if sd.spans == nil {
		sd.spans = make(map[*Span]struct{})
	}
	sd.spans[sp] = struct{}{}
	sd.reads = append(sd.reads, Range{Off: sp.off, Len: sp.length})
}

// Copy returns an independent, mutable copy of [off, off+n). Parsers
// that need to modify input bytes must use this; the copy is private
// to the caller and never aliases the shared input.
func (sd *Side) Copy(off, n int64) ([]byte, error) {
	sp, err := sd.Borrow(off, n)
	if err != nil {
		return nil, err
	}
	defer sp.Release()
	out := make([]byte, 0, n)
	for _, sg := range sp.segs {
		out = append(out, sg.view...)
	}
	sd.src.copied.Add(int64(len(out)))
	return out, nil
}

// Cancel releases every span this side still holds and refuses further
// borrows. Blocks still borrowed by other sides stay alive.
func (sd *Side) Cancel() {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	if sd.cancelled {
		return
	}
	sd.cancelled = true
	for sp := range sd.spans {
		sp.release()
	}
	sd.spans = nil
}

// ReadRanges returns the ranges this side has borrowed so far.
func (sd *Side) ReadRanges() []Range {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	out := make([]Range, len(sd.reads))
	copy(out, sd.reads)
	return out
}

// Evidence captures this side's attestation: the digest of the initial
// input it parsed from, and the ranges it actually read.
func (sd *Side) Evidence() Evidence {
	return Evidence{Side: sd.name, Digest: sd.src.Digest(), Ranges: sd.ReadRanges()}
}

func (sd *Side) untrack(sp *Span) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	delete(sd.spans, sp)
}

type segment struct {
	blk  *block
	view []byte
}

// Span is a read-only, zero-copy view into the shared input. The bytes
// exposed by Segments and At must be treated as immutable; parsers that
// need to write must use Side.Copy instead.
type Span struct {
	side     *Side
	off      int64
	length   int64
	segs     []segment
	released atomic.Bool
}

// Len is the number of bytes in the span.
func (sp *Span) Len() int64 { return sp.length }

// At returns the byte at logical index i within the span.
func (sp *Span) At(i int64) byte {
	if i < 0 || i >= sp.length {
		panic("isoinput: span index out of range")
	}
	for _, sg := range sp.segs {
		if i < int64(len(sg.view)) {
			return sg.view[i]
		}
		i -= int64(len(sg.view))
	}
	panic("isoinput: span index out of range")
}

// Segments returns the span as one or more read-only byte slices, one
// per underlying block. A span inside a single block yields exactly one
// segment and involves no copying.
func (sp *Span) Segments() [][]byte {
	out := make([][]byte, len(sp.segs))
	for i, sg := range sp.segs {
		out[i] = sg.view
	}
	return out
}

// NewReader returns an io.Reader over the span without copying.
func (sp *Span) NewReader() io.Reader {
	readers := make([]io.Reader, 0, len(sp.segs))
	for _, sg := range sp.segs {
		readers = append(readers, bytes.NewReader(sg.view))
	}
	return io.MultiReader(readers...)
}

// Release drops this span's references. Blocks shared with other sides
// are not reclaimed until their last borrower releases.
func (sp *Span) Release() {
	if sp.released.CompareAndSwap(false, true) {
		sp.release()
		sp.side.untrack(sp)
	}
}

func (sp *Span) release() {
	for _, sg := range sp.segs {
		if sg.blk.refs.Add(-1) == 0 {
			sg.blk.reclaimed.Store(true)
		}
	}
}
