package ringpool

import (
	"encoding/binary"
	"fmt"
	"io"
)

// HeaderLen is the frame length prefix size in bytes.
const HeaderLen = 4

// MaxFrameSize bounds a single frame payload (1 MiB).
const MaxFrameSize = 1 << 20

// FrameWriter writes length-prefixed frames to a stream.
type FrameWriter struct {
	w   io.Writer
	hdr [HeaderLen]byte
}

// NewFrameWriter returns a FrameWriter writing to w.
func NewFrameWriter(w io.Writer) *FrameWriter { return &FrameWriter{w: w} }

// WriteFrame writes one length-prefixed frame.
func (fw *FrameWriter) WriteFrame(payload []byte) error {
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("ringpool: frame of %d bytes exceeds MaxFrameSize", len(payload))
	}
	binary.BigEndian.PutUint32(fw.hdr[:], uint32(len(payload)))
	if _, err := fw.w.Write(fw.hdr[:]); err != nil {
		return err
	}
	_, err := fw.w.Write(payload)
	return err
}

// FrameReader is the zero-copy slice multiplexer: it cuts frames out of
// a stream directly into ring-pool Slices without copying payload bytes.
type FrameReader struct {
	pool *Pool
	r    io.Reader
	hdr  [HeaderLen]byte
}

// NewFrameReader returns a FrameReader cutting frames from r into pool.
func NewFrameReader(pool *Pool, r io.Reader) *FrameReader {
	return &FrameReader{pool: pool, r: r}
}

// Next reads the next frame and returns it as a pooled Slice. The
// payload is read straight into the ring buffer; the caller owns the
// returned reference and must Release it. io.EOF is returned at a clean
// stream end.
func (fr *FrameReader) Next() (*Slice, error) {
	if _, err := io.ReadFull(fr.r, fr.hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(fr.hdr[:])
	if n == 0 || n > MaxFrameSize {
		return nil, fmt.Errorf("ringpool: invalid frame length %d", n)
	}
	res, err := fr.pool.Reserve(int(n))
	if err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(fr.r, res.Buf); err != nil {
		res.Cancel()
		return nil, err
	}
	return res.Commit(), nil
}
