package netsec

import (
	"errors"
	"io"
	"math/bits"
)

// Stream wraps a random byte source and records how many bytes have been
// consumed. It lets callers inspect the exact random-stream position after a
// successful draw, an error, or a cancellation.
type Stream struct {
	r        io.Reader
	position int64
}

// NewStream wraps r and starts counting consumed bytes at zero.
func NewStream(r io.Reader) *Stream {
	return &Stream{r: r}
}

// Read pulls random bytes from the underlying source and advances the
// position by the number actually delivered. A short read is reported as an
// error, so partial consumption is still reflected by Position.
func (st *Stream) Read(buf []byte) (int, error) {
	n, err := io.ReadFull(st.r, buf)
	st.position += int64(n)
	return n, err
}

// Position reports how many random bytes have been consumed so far.
func (st *Stream) Position() int64 {
	return st.position
}

// Sample draws exactly one legal layout uniformly at random.
//
// Uniformity is exact: the legal layouts are counted first, a uniform index
// in [0,count) is produced with integer-level rejection sampling (never a
// layout-level retry loop), and the index is mapped to a layout through
// Unrank. When no legal layout exists the error is ErrNoLayout and no random
// bytes are consumed.
func (s *Spec) Sample(st *Stream) (Layout, error) {
	if st == nil {
		return nil, errors.New("netsec: nil random stream")
	}
	total, err := s.Count()
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, ErrNoLayout
	}
	if total == 1 {
		return s.Unrank(0)
	}
	index, err := uniformIndex(st, total)
	if err != nil {
		return nil, err
	}
	return s.Unrank(index)
}

// uniformIndex returns an integer uniformly distributed over [0,n) using
// rejection sampling on whole random integers. Rejected draws do consume
// bytes, which is exactly the rejection rule that preserves uniformity; no
// layout is ever rejected.
func uniformIndex(st *Stream, n int64) (int64, error) {
	width := byte((bits.Len64(uint64(n-1)) + 7) / 8)
	limit := uint64(1) << (width * 8)
	cutoff := limit - limit%uint64(n)
	buf := make([]byte, width)
	for {
		if _, err := st.Read(buf); err != nil {
			return 0, err
		}
		var value uint64
		for _, b := range buf {
			value = value<<8 | uint64(b)
		}
		if value < cutoff {
			return int64(value % uint64(n)), nil
		}
	}
}
