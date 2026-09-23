package slz

import (
	"errors"
	"math/bits"
)

// bitWriter appends bits MSB-first.
type bitWriter struct {
	buf []byte
	cur byte
	n   uint
}

func (w *bitWriter) writeBit(b uint32) {
	w.cur = w.cur<<1 | byte(b&1)
	w.n++
	if w.n == 8 {
		w.buf = append(w.buf, w.cur)
		w.cur = 0
		w.n = 0
	}
}

func (w *bitWriter) writeBits(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		w.writeBit(v >> uint(i))
	}
}

// writeGamma writes Elias gamma code of v (v >= 1).
func (w *bitWriter) writeGamma(v int) {
	k := bits.Len(uint(v)) - 1
	for i := 0; i < k; i++ {
		w.writeBit(0)
	}
	w.writeBits(uint32(v), k+1)
}

// flush pads the final partial byte with zero bits.
func (w *bitWriter) flush() {
	if w.n > 0 {
		w.buf = append(w.buf, w.cur<<(8-w.n))
		w.cur = 0
		w.n = 0
	}
}

var errUnexpectedEOF = errors.New("slz: unexpected end of stream")

// bitReader reads bits MSB-first.
type bitReader struct {
	data []byte
	pos  int // bit position
}

func (r *bitReader) alignByte() { r.pos = (r.pos + 7) &^ 7 }

func (r *bitReader) readBit() (uint32, error) {
	if r.pos/8 >= len(r.data) {
		return 0, errUnexpectedEOF
	}
	b := (r.data[r.pos/8] >> uint(7-r.pos%8)) & 1
	r.pos++
	return uint32(b), nil
}

func (r *bitReader) readBits(n int) (uint32, error) {
	var v uint32
	for i := 0; i < n; i++ {
		b, err := r.readBit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | b
	}
	return v, nil
}

// readGamma reads an Elias gamma coded value >= 1.
func (r *bitReader) readGamma() (int, error) {
	k := 0
	for {
		b, err := r.readBit()
		if err != nil {
			return 0, err
		}
		if b == 1 {
			break
		}
		k++
		if k > 30 {
			return 0, errors.New("slz: gamma code too large")
		}
	}
	rest, err := r.readBits(k)
	if err != nil {
		return 0, err
	}
	return 1<<k | int(rest), nil
}

func (r *bitReader) readByteAligned() (byte, error) {
	if r.pos%8 != 0 {
		return 0, errors.New("slz: internal misalignment")
	}
	if r.pos/8 >= len(r.data) {
		return 0, errUnexpectedEOF
	}
	b := r.data[r.pos/8]
	r.pos += 8
	return b, nil
}

func (r *bitReader) readUvarint() (uint64, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		b, err := r.readByteAligned()
		if err != nil {
			return 0, err
		}
		v |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, errors.New("slz: varint overflow")
}
