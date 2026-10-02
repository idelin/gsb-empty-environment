package floatwire

// Stream encoding: a sequence of fixed-width records. A StreamDecoder
// accepts arbitrarily chunked input and emits bit-identical values; the
// chunk boundaries never influence the decoded bits.

// StreamEncoder appends fixed-width records to a byte stream.
type StreamEncoder struct {
	w     Width
	order Order
	buf   []byte
	err   error
}

// NewStreamEncoder creates an encoder for width w. Unsupported widths are
// reported on the first Write and by Err.
func NewStreamEncoder(w Width, order Order) *StreamEncoder {
	se := &StreamEncoder{w: w, order: order}
	if !w.Valid() {
		se.err = ErrUnsupportedWidth
	}
	return se
}

// Write appends one value (the low w bits of bits) to the stream.
func (e *StreamEncoder) Write(bits uint64) error {
	if e.err != nil {
		return e.err
	}
	var tmp [8]byte
	put(e.order, tmp[:e.w.Bytes()], bits)
	e.buf = append(e.buf, tmp[:e.w.Bytes()]...)
	return nil
}

// Err reports the first error encountered.
func (e *StreamEncoder) Err() error { return e.err }

// Bytes returns the encoded stream so far.
func (e *StreamEncoder) Bytes() []byte { return e.buf }

// StreamDecoder reassembles fixed-width records from arbitrarily chunked
// input. Decoding is pure bit assembly; no host code is executed and no
// allocation happens beyond the pending partial record.
type StreamDecoder struct {
	w       Width
	order   Order
	pending []byte
	err     error
}

// NewStreamDecoder creates a decoder for width w.
func NewStreamDecoder(w Width, order Order) *StreamDecoder {
	sd := &StreamDecoder{w: w, order: order}
	if !w.Valid() {
		sd.err = ErrUnsupportedWidth
	}
	return sd
}

// Feed consumes one arbitrary chunk and returns every complete value it
// finishes, in order. Leftover bytes are kept for the next Feed.
func (d *StreamDecoder) Feed(chunk []byte) ([]uint64, error) {
	if d.err != nil {
		return nil, d.err
	}
	d.pending = append(d.pending, chunk...)
	need := d.w.Bytes()
	n := len(d.pending) / need
	out := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, get(d.order, d.pending[i*need:(i+1)*need]))
	}
	d.pending = append([]byte(nil), d.pending[n*need:]...)
	return out, nil
}

// Finish reports any trailing partial record as an error and returns the
// remaining undelivered bytes count.
func (d *StreamDecoder) Finish() error {
	if d.err != nil {
		return d.err
	}
	if len(d.pending) != 0 {
		return ErrShortBuffer
	}
	return nil
}

// Frame format for self-describing records:
//
//	[magic 0xF1][width byte: 16/32/64][payload: width/8 bytes big-endian]
//
// The decoder validates the header and the available length before
// allocating or touching the payload.
const (
	frameMagic = 0xF1
	frameHead  = 2
)

// EncodeFrame wraps one value in a self-describing frame.
func EncodeFrame(w Width, bits uint64) ([]byte, error) {
	if !w.Valid() {
		return nil, ErrUnsupportedWidth
	}
	out := make([]byte, frameHead+w.Bytes())
	out[0] = frameMagic
	out[1] = byte(w)
	put(BigEndian, out[frameHead:], bits)
	return out, nil
}

// DecodeFrame decodes one frame from the front of data and reports how
// many bytes it consumed. The declared width is validated before any
// payload byte is read, and the buffer length is validated before any
// allocation: a truncated or malformed frame never causes a read past
// the input.
func DecodeFrame(data []byte) (bits uint64, consumed int, err error) {
	if len(data) < frameHead {
		return 0, 0, ErrShortBuffer
	}
	if data[0] != frameMagic {
		return 0, 0, ErrBadFrame
	}
	w := Width(data[1])
	if !w.Valid() {
		return 0, 0, ErrUnsupportedWidth
	}
	if len(data) < frameHead+w.Bytes() {
		return 0, 0, ErrShortBuffer
	}
	bits, err = Decode(w, data[frameHead:frameHead+w.Bytes()])
	if err != nil {
		return 0, 0, err
	}
	return bits, frameHead + w.Bytes(), nil
}
