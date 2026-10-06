package bitstream

import "errors"

var (
	// ErrCapacityExceeded is returned when a write would exceed the
	// writer capacity. The writer is left unchanged.
	ErrCapacityExceeded = errors.New("bitstream: capacity exceeded")
	// ErrStaleBookmark is returned when a bookmark created before a
	// rollback is used.
	ErrStaleBookmark = errors.New("bitstream: stale bookmark (created before a rollback)")
	// ErrRegionClosed is returned when operating on a closed region.
	ErrRegionClosed = errors.New("bitstream: region already closed")
	// ErrRegionOrder is returned when regions are not closed in LIFO order.
	ErrRegionOrder = errors.New("bitstream: regions must be closed in LIFO (nested) order")
	// ErrLengthOverflow is returned when region content does not fit the
	// reserved length field.
	ErrLengthOverflow = errors.New("bitstream: region content length overflows reserved field")
	// ErrInvalidCursor is returned when the cursor moved before a region start.
	ErrInvalidCursor = errors.New("bitstream: cursor moved before region content")
)

// Unlimited removes the capacity limit when used as a capacity.
const Unlimited = -1

// Writer writes bits MSB-first. Writing may overwrite existing bits
// (never touching neighbouring bits) or append new ones, bounded by a
// capacity in bits.
type Writer struct {
	bits     []bool
	cursor   int
	capacity int // max length in bits; Unlimited means unbounded
	version  uint64
	regions  []*Region
}

// NewWriter returns an empty writer that can hold up to capacityBits
// bits (Unlimited for no limit).
func NewWriter(capacityBits int) *Writer {
	return &Writer{capacity: capacityBits}
}

// NewWriterFromBytes returns a writer initialised with the bits of data
// (MSB-first). capacityBits bounds the total length; pass Unlimited to
// allow unbounded growth. A capacity below len(data)*8 is raised to fit.
func NewWriterFromBytes(data []byte, capacityBits int) *Writer {
	w := &Writer{capacity: capacityBits}
	w.bits = make([]bool, len(data)*8)
	for i := range w.bits {
		w.bits[i] = data[i>>3]>>uint(7-(i&7))&1 == 1
	}
	if w.capacity >= 0 && w.capacity < len(w.bits) {
		w.capacity = len(w.bits)
	}
	return w
}

// Len returns the current content length in bits.
func (w *Writer) Len() int { return len(w.bits) }

// Pos returns the cursor bit position.
func (w *Writer) Pos() int { return w.cursor }

// Bookmark marks a writer position. A bookmark is tied to the writer
// version: any region Abort invalidates all earlier bookmarks.
type Bookmark struct {
	pos     int
	version uint64
}

// Bookmark returns a bookmark for the current cursor position.
func (w *Writer) Bookmark() Bookmark { return Bookmark{pos: w.cursor, version: w.version} }

// Seek moves the cursor to a bookmark. It fails with ErrStaleBookmark if
// a rollback happened after the bookmark was created.
func (w *Writer) Seek(b Bookmark) error {
	if b.version != w.version {
		return ErrStaleBookmark
	}
	w.cursor = b.pos
	return nil
}

// writeAt writes n bits of v (MSB-first) starting at pos, overwriting
// existing bits and never touching neighbours. Callers must have checked
// capacity.
func (w *Writer) writeAt(pos int, v uint64, n int) {
	for i := 0; i < n; i++ {
		bit := v>>uint(n-1-i)&1 == 1
		p := pos + i
		for len(w.bits) < p {
			w.bits = append(w.bits, false)
		}
		if p < len(w.bits) {
			w.bits[p] = bit
		} else {
			w.bits = append(w.bits, bit)
		}
	}
}

// WriteBits writes the low n (1..64) bits of v, MSB-first, at the cursor
// and advances the cursor. Existing bits under the cursor are
// overwritten; neighbouring bits are never modified. If the write would
// exceed the capacity, ErrCapacityExceeded is returned and the writer is
// left unchanged.
func (w *Writer) WriteBits(v uint64, n int) error {
	if n < 1 || n > 64 {
		return ErrInvalidBitCount
	}
	if w.capacity >= 0 && w.cursor+n > w.capacity {
		return ErrCapacityExceeded
	}
	w.writeAt(w.cursor, v, n)
	w.cursor += n
	return nil
}

// WriteSigned writes the low n bits of v (two's complement), MSB-first.
func (w *Writer) WriteSigned(v int64, n int) error {
	return w.WriteBits(uint64(v), n)
}

// Bytes packs the content MSB-first. Trailing padding bits are zero.
func (w *Writer) Bytes() []byte {
	out := make([]byte, (len(w.bits)+7)/8)
	for i, b := range w.bits {
		if b {
			out[i>>3] |= 1 << uint(7-(i&7))
		}
	}
	return out
}

// Region is a nested length-reservation region: a length field is
// reserved on BeginRegion, content is written after it, and Close
// backfills the content length into the reserved field. Abort rolls the
// writer back to the region start, restoring the original bits.
type Region struct {
	w         *Writer
	fieldBits int
	start     int    // bit position of the reserved field
	saved     []bool // snapshot of w.bits[start:] at BeginRegion
	closed    bool
}

// BeginRegion reserves a zero-filled length field of fieldBits (1..64)
// at the cursor and returns the open region. Regions nest; they must be
// closed or aborted in LIFO order.
func (w *Writer) BeginRegion(fieldBits int) (*Region, error) {
	if fieldBits < 1 || fieldBits > 64 {
		return nil, ErrInvalidBitCount
	}
	r := &Region{
		w:         w,
		fieldBits: fieldBits,
		start:     w.cursor,
		saved:     append([]bool(nil), w.bits[w.cursor:]...),
	}
	if err := w.WriteBits(0, fieldBits); err != nil {
		return nil, err
	}
	w.regions = append(w.regions, r)
	return r, nil
}

// Close backfills the number of content bits written since BeginRegion
// into the reserved field, overwriting only the field's own bits, and
// seals the region.
func (r *Region) Close() error {
	w := r.w
	if r.closed {
		return ErrRegionClosed
	}
	if n := len(w.regions); n == 0 || w.regions[n-1] != r {
		return ErrRegionOrder
	}
	content := w.cursor - r.start - r.fieldBits
	if content < 0 {
		return ErrInvalidCursor
	}
	if r.fieldBits < 64 && uint64(content) >= 1<<uint(r.fieldBits) {
		return ErrLengthOverflow
	}
	w.writeAt(r.start, uint64(content), r.fieldBits)
	w.regions = w.regions[:len(w.regions)-1]
	r.closed = true
	return nil
}

// Abort rolls the writer back to the region start: bits appended or
// overwritten since BeginRegion are discarded and the original bits
// (including neighbours of the reserved field) are restored. Any open
// inner regions are aborted too, and all bookmarks created so far become
// stale.
func (r *Region) Abort() {
	w := r.w
	if r.closed {
		return
	}
	for len(w.regions) > 0 {
		top := w.regions[len(w.regions)-1]
		w.regions = w.regions[:len(w.regions)-1]
		top.closed = true
		if top == r {
			break
		}
	}
	w.bits = append(w.bits[:r.start:r.start], r.saved...)
	w.cursor = r.start
	w.version++
}
