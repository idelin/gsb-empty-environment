package rlecomp

import (
	"errors"
	"io"
)

// ErrClosed is returned when writing to a closed Encoder.
var ErrClosed = errors.New("rlecomp: encoder closed")

// Compress encodes src into a self-contained stream using the offline
// optimal splitter. Empty input yields exactly one empty frame.
//
// Worst-case size for n >= 1 source bytes is
// n + MaxHeaderLen + frameOverhead (single frame, n <= MaxFramePayload),
// i.e. expansion is bounded by 17 bytes.
func Compress(src []byte) []byte {
	var stream, payload []byte
	for _, s := range segment(src) {
		payload = appendBlock(payload, src, s)
		if len(payload) >= MaxFramePayload {
			stream = appendFrame(stream, payload)
			payload = payload[:0]
		}
	}
	return appendFrame(stream, payload)
}

// Encoder is a streaming compressor with a bounded lookahead buffer.
// At most window source bytes are held undecided; everything older is
// already optimally segmented and queued for output. Because the encoder
// cannot see future input, each window boundary (and each Flush) forces a
// block boundary: compared to the offline optimum the output grows by at
// most (MaxHeaderLen+1) bytes per forced boundary, i.e.
//
//	online <= offline + (MaxHeaderLen+1) * (ceil(n/window) + flushes)
//
// All output first goes to an internal pending queue; if the underlying
// writer blocks (short write or error) the unwritten bytes are kept and
// retried by the next Write/Flush/Resume call, never duplicated.
type Encoder struct {
	w       io.Writer
	window  int
	buf     []byte // undecided source bytes (<= window after Write returns)
	payload []byte // encoded blocks of the current frame
	pending []byte // framed output not yet accepted by w
	frames  int    // frames emitted so far
	closed  bool
}

// NewEncoder returns a streaming encoder holding at most window
// undecided source bytes.
func NewEncoder(w io.Writer, window int) *Encoder {
	if window < 1 {
		window = 1
	}
	return &Encoder{w: w, window: window}
}

// Write buffers p, finalizing every block that cannot be extended by
// future input. On error p is still consumed; call Resume (or Flush) to
// retry the blocked output.
func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrClosed
	}
	e.buf = append(e.buf, p...)
	e.drainWindow()
	if err := e.flushPending(); err != nil {
		return len(p), err
	}
	return len(p), nil
}

// Flush encodes all buffered input and ends the current frame. It is an
// explicit boundary: nothing about future input is assumed. With no
// pending data it emits one empty frame only if no frame was ever
// emitted, so empty input is distinguishable from no frame. Flush does
// not end the stream; more frames may follow.
func (e *Encoder) Flush() error {
	if e.closed {
		return ErrClosed
	}
	for _, s := range segment(e.buf) {
		e.payload = appendBlock(e.payload, e.buf, s)
		if len(e.payload) >= MaxFramePayload {
			e.endFrame()
		}
	}
	e.buf = e.buf[:0]
	if len(e.payload) > 0 || e.frames == 0 {
		e.endFrame()
	}
	return e.flushPending()
}

// Resume retries writing queued output after a previous error.
func (e *Encoder) Resume() error {
	if e.closed {
		return ErrClosed
	}
	return e.flushPending()
}

// Close flushes and closes the encoder. The stream itself ends at EOF.
func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	if err := e.Flush(); err != nil {
		return err
	}
	e.closed = true
	return nil
}

// drainWindow finalizes the safe prefix of buf: everything except the
// trailing run, which future input may still extend.
func (e *Encoder) drainWindow() {
	for len(e.buf) > e.window {
		if t := trailingRunStart(e.buf); t > 0 {
			for _, s := range segment(e.buf[:t]) {
				e.payload = appendBlock(e.payload, e.buf, s)
			}
			e.buf = e.buf[t:]
		} else {
			// The whole buffer is one run; emit a maximal repeat
			// block. A continuing run simply starts a new block.
			n := min(len(e.buf), MaxBlockLen)
			e.payload = appendBlock(e.payload, e.buf, seg{repeat: true, length: n})
			e.buf = e.buf[n:]
		}
		if len(e.payload) >= MaxFramePayload {
			e.endFrame()
		}
	}
}

func (e *Encoder) endFrame() {
	e.pending = appendFrame(e.pending, e.payload)
	e.payload = e.payload[:0]
	e.frames++
}

// flushPending writes as much queued output as the sink accepts. Bytes
// leave the queue only after the sink confirms them, so a blocked writer
// never causes duplication.
func (e *Encoder) flushPending() error {
	for len(e.pending) > 0 {
		n, err := e.w.Write(e.pending)
		e.pending = e.pending[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// WorstCaseBound returns an upper bound in bytes on the total stream
// size produced by an Encoder holding window undecided source bytes for
// n source bytes and the given number of explicit flushes: the offline
// optimum plus (MaxHeaderLen+1) per forced boundary, plus per-frame
// overhead.
func WorstCaseBound(n, window, flushes int) int {
	if window < 1 {
		window = 1
	}
	boundaries := (n+window-1)/window + flushes
	frames := flushes + 1 + n/MaxFramePayload + 1
	return n + MaxHeaderLen + (MaxHeaderLen+1)*boundaries + frameOverhead*frames
}
