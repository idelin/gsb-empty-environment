// Package rangecoder implements a carry-less range coder with an adaptive
// order-0 model, checkpointable model state, random-access decoding from
// checkpoints, and resumable encoding. Only the Go standard library is used;
// no third-party compression library is involved.
package rangecoder

import "bytes"

// Carry-less range coder constants (Subbotin-style).
const (
	rcTop uint32 = 1 << 24
	rcBot uint32 = 1 << 16
)

// rangeEncoder is the low-level carry-less range encoder. It writes bytes to
// out. Use newRangeEncoder to construct one.
type rangeEncoder struct {
	low, rng uint32
	out      *bytes.Buffer
}

func newRangeEncoder(out *bytes.Buffer) *rangeEncoder {
	return &rangeEncoder{rng: 0xFFFFFFFF, out: out}
}

// encode encodes one symbol described by its cumulative frequency, its own
// frequency and the total frequency of the model.
func (e *rangeEncoder) encode(cum, freq, tot uint32) {
	e.rng /= tot
	e.low += cum * e.rng
	e.rng *= freq
	for {
		if (e.low ^ (e.low + e.rng)) >= rcTop {
			if e.rng < rcBot {
				// Interval straddles a boundary: clamp the range to the
				// next rcBot-aligned boundary instead of propagating carry.
				e.rng = -e.low & (rcBot - 1)
			} else {
				break
			}
		}
		e.out.WriteByte(byte(e.low >> 24))
		e.low <<= 8
		e.rng <<= 8
	}
}

// flush emits the final 4 bytes of state so the decoder can terminate. After
// flush the encoder must not be used again; start a fresh one instead.
func (e *rangeEncoder) flush() {
	for i := 0; i < 4; i++ {
		e.out.WriteByte(byte(e.low >> 24))
		e.low <<= 8
	}
}

// rangeDecoder is the low-level carry-less range decoder. It mirrors
// rangeEncoder exactly, tracking the same low/rng values.
type rangeDecoder struct {
	low, rng, code uint32
	data           []byte
	pos            int
}

func newRangeDecoder(data []byte, off int) *rangeDecoder {
	d := &rangeDecoder{rng: 0xFFFFFFFF, data: data, pos: off}
	for i := 0; i < 4; i++ {
		d.code = d.code<<8 | d.next()
	}
	return d
}

// next returns the next input byte, or 0 when the stream is exhausted
// (zero padding past the end, matching the encoder's flush).
func (d *rangeDecoder) next() uint32 {
	var b byte
	if d.pos < len(d.data) {
		b = d.data[d.pos]
	}
	d.pos++
	return uint32(b)
}

// freq returns the cumulative frequency count for the next symbol.
func (d *rangeDecoder) freq(tot uint32) uint32 {
	c := d.code / (d.rng / tot)
	if c >= tot {
		// Can only happen while reading zero padding past the stream end.
		c = tot - 1
	}
	return c
}

// decode consumes one symbol described by cum/freq/tot.
func (d *rangeDecoder) decode(cum, freq, tot uint32) {
	d.rng /= tot
	d.low += cum * d.rng
	d.code -= cum * d.rng
	d.rng *= freq
	for {
		if (d.low ^ (d.low + d.rng)) >= rcTop {
			if d.rng < rcBot {
				d.rng = -d.low & (rcBot - 1)
			} else {
				break
			}
		}
		d.code = d.code<<8 | d.next()
		d.low <<= 8
		d.rng <<= 8
	}
}
