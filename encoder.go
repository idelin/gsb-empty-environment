package runlzv2

import "io"

// Encoder is an online compressor with a bounded input buffer. It never
// inspects future bytes: a full buffer or an explicit Flush forces a frame.
type Encoder struct {
	w      io.Writer
	buf    []byte
	limit  int
	closed bool
	err    error
}

// NewEncoder returns an online encoder with DefaultOnlineBuffer.
func NewEncoder(w io.Writer) *Encoder {
	e, _ := NewEncoderBuffer(w, DefaultOnlineBuffer)
	return e
}

// NewEncoderBuffer returns an online encoder whose input buffer is bounded
// by bufferSize (1..MaxBlockLen).
func NewEncoderBuffer(w io.Writer, bufferSize int) (*Encoder, error) {
	if w == nil || bufferSize < 1 || bufferSize > MaxBlockLen {
		return nil, ErrInvalidArgument
	}
	return &Encoder{w: w, buf: make([]byte, 0, bufferSize), limit: bufferSize}, nil
}

// Write buffers p, emitting a non-final frame whenever the buffer fills.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	if e.closed {
		return 0, ErrClosed
	}

	written := 0
	for len(p) > 0 {
		n := e.limit - len(e.buf)
		if n > len(p) {
			n = len(p)
		}
		e.buf = append(e.buf, p[:n]...)
		p = p[n:]
		written += n

		if len(e.buf) == e.limit {
			if err := e.emit(0); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// Flush emits all buffered bytes as an explicit flush-boundary frame.
// An empty buffer produces an empty flush frame. Flush never ends the stream.
func (e *Encoder) Flush() error {
	if e.err != nil {
		return e.err
	}
	if e.closed {
		return ErrClosed
	}
	return e.emit(FlagFlush)
}

// Close flushes remaining bytes as the final frame. If nothing was ever
// written, Close emits an empty final frame so a clean stream end exists.
func (e *Encoder) Close() error {
	if e.err != nil {
		return e.err
	}
	if e.closed {
		return ErrClosed
	}
	e.closed = true
	return e.emit(FlagFinal)
}

func (e *Encoder) emit(flags byte) error {
	var payload []byte
	if len(e.buf) > 0 {
		payload = encodeBlocks(e.buf, planOptimal(e.buf))
	}
	frame, err := encodeFrame(payload, flags)
	if err != nil {
		e.err = err
		return err
	}
	if _, err := e.w.Write(frame); err != nil {
		e.err = err
		return err
	}
	e.buf = e.buf[:0]
	return nil
}

// Encode compresses src offline into a single final frame using the optimal
// segmentation. Empty input returns nil (no frame), distinct from an empty
// flush frame produced by the streaming API.
func Encode(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return nil, nil
	}
	return encodeFrame(encodeBlocks(src, planOptimal(src)), FlagFinal)
}
