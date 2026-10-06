package nbf

import (
	"encoding/binary"
	"fmt"
)

// reader is a bounds-checked cursor over a byte range. A reader created
// by sub() covers exactly the nested body's bytes, so nested parsing can
// never read past the enclosing (parent) range. base is the absolute
// offset of buf[0] within the original file, used for error positions.
type reader struct {
	buf   []byte
	off   int
	base  int
	order binary.ByteOrder
}

func (r *reader) remaining() int { return len(r.buf) - r.off }

func (r *reader) failf(format string, args ...interface{}) error {
	return &ParseError{Offset: r.base + r.off, Msg: fmt.Sprintf(format, args...)}
}

// read consumes n bytes, failing if fewer than n remain in this range.
func (r *reader) read(n int) ([]byte, error) {
	if n < 0 || n > r.remaining() {
		return nil, r.failf("need %d bytes but only %d remain in enclosing range (truncated or length prefix overruns parent)", n, r.remaining())
	}
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b, nil
}

// sub consumes n bytes and returns a reader bounded to exactly those
// bytes, sharing the byte order and carrying absolute offsets.
func (r *reader) sub(n int) (*reader, error) {
	start := r.off
	b, err := r.read(n)
	if err != nil {
		return nil, err
	}
	return &reader{buf: b, base: r.base + start, order: r.order}, nil
}

func (r *reader) u8() (byte, error) {
	b, err := r.read(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *reader) u16() (uint16, error) {
	b, err := r.read(2)
	if err != nil {
		return 0, err
	}
	return r.order.Uint16(b), nil
}

func (r *reader) u32() (uint32, error) {
	b, err := r.read(4)
	if err != nil {
		return 0, err
	}
	return r.order.Uint32(b), nil
}

func (r *reader) u64() (uint64, error) {
	b, err := r.read(8)
	if err != nil {
		return 0, err
	}
	return r.order.Uint64(b), nil
}
