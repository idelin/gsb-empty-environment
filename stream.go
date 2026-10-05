package wsample

import (
	"fmt"
	"io"
)

// Stream supplies the random bytes that drive sampling. The caller owns the
// stream: it decides where the bytes come from (a file, a PRNG, a hardware
// source, a fixed test vector to replay) and is responsible for positioning it.
//
// Pos must report the total number of bytes consumed so far, so that a
// Snapshot can record the exact position of the random stream.
type Stream interface {
	io.Reader
	// Pos returns the number of bytes consumed from the stream so far.
	Pos() uint64
}

// BytesStream is an in-memory Stream over a fixed byte slice. It supports
// seeking, which makes it suitable for replay and for restoring a snapshot.
type BytesStream struct {
	data []byte
	off  uint64
}

// NewBytesStream returns a Stream that reads from data.
func NewBytesStream(data []byte) *BytesStream {
	return &BytesStream{data: data}
}

// Read implements io.Reader.
func (s *BytesStream) Read(p []byte) (int, error) {
	if s.off >= uint64(len(s.data)) {
		return 0, io.EOF
	}
	n := copy(p, s.data[s.off:])
	s.off += uint64(n)
	return n, nil
}

// Pos returns the number of bytes consumed so far.
func (s *BytesStream) Pos() uint64 { return s.off }

// Seek repositions the stream to an absolute byte offset.
func (s *BytesStream) Seek(pos uint64) error {
	if pos > uint64(len(s.data)) {
		return fmt.Errorf("wsample: seek offset %d beyond stream length %d", pos, len(s.data))
	}
	s.off = pos
	return nil
}

// CountingStream wraps an arbitrary io.Reader and tracks how many bytes have
// been consumed. It cannot seek; to restore a snapshot the caller must supply
// an underlying reader already positioned at the snapshot's offset.
type CountingStream struct {
	r io.Reader
	n uint64
}

// NewCountingStream wraps r, counting consumed bytes from zero.
func NewCountingStream(r io.Reader) *CountingStream {
	return &CountingStream{r: r}
}

// Read implements io.Reader.
func (c *CountingStream) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += uint64(n)
	return n, err
}

// Pos returns the number of bytes consumed so far.
func (c *CountingStream) Pos() uint64 { return c.n }
