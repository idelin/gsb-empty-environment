// Package bitstream provides MSB-first bit-level reading and writing.
package bitstream

import "errors"

var (
	// ErrInvalidBitCount is returned when a bit count is outside [1, 64].
	ErrInvalidBitCount = errors.New("bitstream: bit count must be between 1 and 64")
	// ErrNotEnoughBits is returned when a read would pass the end of the
	// data. The reader position is left unchanged.
	ErrNotEnoughBits = errors.New("bitstream: not enough bits")
)

// Reader reads bits MSB-first from a byte slice.
type Reader struct {
	data []byte
	pos  int // bit position, 0 is the MSB of data[0]
}

// NewReader returns a Reader over data.
func NewReader(data []byte) *Reader { return &Reader{data: data} }

// Len returns the total number of readable bits.
func (r *Reader) Len() int { return len(r.data) * 8 }

// Pos returns the current bit position.
func (r *Reader) Pos() int { return r.pos }

// Remaining returns the number of unread bits.
func (r *Reader) Remaining() int { return r.Len() - r.pos }

// ReadBookmark marks a reader position for later Reset.
type ReadBookmark int

// Bookmark returns a bookmark for the current position.
func (r *Reader) Bookmark() ReadBookmark { return ReadBookmark(r.pos) }

// Reset moves the reader back to a bookmark. Out-of-range bookmarks are
// clamped to [0, Len].
func (r *Reader) Reset(b ReadBookmark) {
	if b < 0 {
		b = 0
	}
	if int(b) > r.Len() {
		b = ReadBookmark(r.Len())
	}
	r.pos = int(b)
}

func (r *Reader) bitAt(i int) uint64 {
	return uint64(r.data[i>>3] >> uint(7-(i&7)) & 1)
}

// ReadBits reads n (1..64) bits MSB-first and returns them right-aligned.
// If fewer than n bits remain, it returns ErrNotEnoughBits and does not
// advance the position.
func (r *Reader) ReadBits(n int) (uint64, error) {
	if n < 1 || n > 64 {
		return 0, ErrInvalidBitCount
	}
	if r.Remaining() < n {
		return 0, ErrNotEnoughBits
	}
	var v uint64
	for i := 0; i < n; i++ {
		v = v<<1 | r.bitAt(r.pos+i)
	}
	r.pos += n
	return v, nil
}

// ReadSigned reads n (1..64) bits MSB-first and sign-extends the two's
// complement value to int64. On error the position is unchanged.
func (r *Reader) ReadSigned(n int) (int64, error) {
	v, err := r.ReadBits(n)
	if err != nil {
		return 0, err
	}
	shift := uint(64 - n)
	return int64(v<<shift) >> shift, nil
}
