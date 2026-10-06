// Package bitstream provides an MSB-first bit-level writer and reader
// with bookmark rollback and nested back-patched length fields.
package bitstream

import "errors"

var (
	ErrBitCount      = errors.New("bitstream: bit count must be between 1 and 64")
	ErrUnderflow     = errors.New("bitstream: not enough bits remaining")
	ErrCapacity      = errors.New("bitstream: capacity exceeded")
	ErrNoRegion      = errors.New("bitstream: no open length region")
	ErrLenOverflow   = errors.New("bitstream: content length does not fit reserved field")
	ErrStaleBookmark = errors.New("bitstream: bookmark invalidated by rollback")
	ErrBadSeek       = errors.New("bitstream: position out of range")
)

// Bookmark identifies a write position. It is bound to the writer's
// version and becomes invalid after any rollback.
type Bookmark struct {
	pos     int
	version uint64
}

type region struct {
	fieldPos  int // bit position of the reserved length field
	width     int // width of the reserved field in bits
	bufSnap   []byte
	nbitsSnap int
}

// Writer appends or overwrites bits MSB-first. Bits past the logical
// length in the final byte are always zero.
type Writer struct {
	buf     []byte
	nbits   int // logical length (high-water mark)
	pos     int // current write position, always <= nbits
	maxBits int // <0 means unlimited
	version uint64
	stack   []region
}

// NewWriter returns a writer without capacity limit.
func NewWriter() *Writer { return &Writer{maxBits: -1} }

// NewWriterCap returns a writer limited to maxBits bits.
func NewWriterCap(maxBits int) *Writer { return &Writer{maxBits: maxBits} }

// Len returns the logical length in bits.
func (w *Writer) Len() int { return w.nbits }

// Pos returns the current write position in bits.
func (w *Writer) Pos() int { return w.pos }

// Bytes returns the written bytes; padding bits in the last byte are zero.
func (w *Writer) Bytes() []byte {
	out := make([]byte, (w.nbits+7)/8)
	copy(out, w.buf)
	return out
}

func (w *Writer) writeBitAt(p int, bit uint64) {
	mask := byte(1 << uint(7-(uint(p)&7)))
	if bit != 0 {
		w.buf[p>>3] |= mask
	} else {
		w.buf[p>>3] &^= mask
	}
}

// WriteBits writes the low n bits of v, MSB-first, at the current
// position. Existing bits are overwritten; neighbouring bits in the
// same byte are preserved. On capacity overflow nothing is modified.
func (w *Writer) WriteBits(v uint64, n int) error {
	if n < 1 || n > 64 {
		return ErrBitCount
	}
	if w.maxBits >= 0 && w.pos+n > w.maxBits {
		return ErrCapacity
	}
	need := (w.pos + n + 7) >> 3
	for len(w.buf) < need {
		w.buf = append(w.buf, 0)
	}
	for i := 0; i < n; i++ {
		w.writeBitAt(w.pos+i, (v>>uint(n-1-i))&1)
	}
	w.pos += n
	if w.pos > w.nbits {
		w.nbits = w.pos
	}
	return nil
}

// WriteSigned writes the two's-complement low n bits of v.
func (w *Writer) WriteSigned(v int64, n int) error {
	return w.WriteBits(uint64(v), n)
}

// Bookmark returns a handle to the current write position.
func (w *Writer) Bookmark() Bookmark { return Bookmark{pos: w.pos, version: w.version} }

// Seek moves the write position to a bookmark, enabling overwrite of
// previously written bits. It fails for stale (pre-rollback) bookmarks.
func (w *Writer) Seek(b Bookmark) error {
	if b.version != w.version {
		return ErrStaleBookmark
	}
	if b.pos < 0 || b.pos > w.nbits {
		return ErrBadSeek
	}
	w.pos = b.pos
	return nil
}

// BeginLenField reserves a zero-filled length field of width bits at the
// current position and opens a region. Content written afterwards is
// measured by EndLenField, which back-patches the field.
func (w *Writer) BeginLenField(width int) error {
	if width < 1 || width > 64 {
		return ErrBitCount
	}
	if w.maxBits >= 0 && w.pos+width > w.maxBits {
		return ErrCapacity
	}
	snap := make([]byte, len(w.buf))
	copy(snap, w.buf)
	r := region{fieldPos: w.pos, width: width, bufSnap: snap, nbitsSnap: w.nbits}
	if err := w.WriteBits(0, width); err != nil {
		return err
	}
	w.stack = append(w.stack, r)
	return nil
}

// EndLenField closes the innermost region and writes the content length
// (in bits) into its reserved field without moving the write position.
func (w *Writer) EndLenField() error {
	if len(w.stack) == 0 {
		return ErrNoRegion
	}
	r := w.stack[len(w.stack)-1]
	w.stack = w.stack[:len(w.stack)-1]
	content := w.pos - (r.fieldPos + r.width)
	if r.width < 64 && uint64(content) >= uint64(1)<<uint(r.width) {
		return ErrLenOverflow
	}
	saved := w.pos
	w.pos = r.fieldPos
	err := w.WriteBits(uint64(content), r.width)
	w.pos = saved
	return err
}

// Abort rolls the innermost region back to its start, restoring the
// exact prior bits (including neighbours sharing a byte) and
// invalidating all outstanding bookmarks.
func (w *Writer) Abort() error {
	if len(w.stack) == 0 {
		return ErrNoRegion
	}
	r := w.stack[len(w.stack)-1]
	w.stack = w.stack[:len(w.stack)-1]
	w.buf = make([]byte, len(r.bufSnap))
	copy(w.buf, r.bufSnap)
	w.nbits = r.nbitsSnap
	w.pos = r.fieldPos
	w.version++
	return nil
}
