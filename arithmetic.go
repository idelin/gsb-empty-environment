// Package arithmetic implements arithmetic coding over a probability model.
//
// The current state interval [Low, High) is maintained with big integers as
// fixed-point rationals with denominator 2^precision. Each symbol subdivides
// the interval according to the model's cumulative distribution, and
// renormalization emits stabilized bits to the output bit stream, so the
// compressed size approaches the Shannon entropy of the source.
package arithmetic

import (
	"encoding/binary"
	"math/big"
)

// precision is the number of fractional bits of the interval endpoints.
const precision = 256

var (
	full         = new(big.Int).Lsh(big.NewInt(1), precision)
	half         = new(big.Int).Lsh(big.NewInt(1), precision-1)
	quarter      = new(big.Int).Lsh(big.NewInt(1), precision-2)
	threeQuarter = new(big.Int).Lsh(big.NewInt(3), precision-2)
)

// bitWriter packs bits MSB-first into bytes.
type bitWriter struct {
	buf   []byte
	acc   byte
	nbits uint
}

func (w *bitWriter) writeBit(bit int) {
	w.acc = w.acc<<1 | byte(bit)
	w.nbits++
	if w.nbits == 8 {
		w.buf = append(w.buf, w.acc)
		w.acc = 0
		w.nbits = 0
	}
}

func (w *bitWriter) bytes() []byte {
	if w.nbits > 0 {
		w.buf = append(w.buf, w.acc<<(8-w.nbits))
		w.acc = 0
		w.nbits = 0
	}
	return w.buf
}

// bitReader reads bits MSB-first; reads past the end yield 0.
type bitReader struct {
	data []byte
	pos  int
}

func (r *bitReader) readBit() int {
	bit := 0
	if r.pos>>3 < len(r.data) {
		bit = int(r.data[r.pos>>3]>>(7-uint(r.pos&7))) & 1
	}
	r.pos++
	return bit
}

// encoder holds the arithmetic coding state interval [low, high).
type encoder struct {
	low, high big.Int
	pending   uint64
	w         bitWriter
}

func newEncoder() *encoder {
	e := &encoder{}
	e.high.Set(full)
	return e
}

// encode narrows [low, high) to the sub-interval
// [low + range*cumLow/total, low + range*cumHigh/total).
func (e *encoder) encode(cumLow, cumHigh, total uint64) {
	var rng, newHigh, newLow, tmp big.Int
	rng.Sub(&e.high, &e.low)
	tot := new(big.Int).SetUint64(total)

	tmp.SetUint64(cumHigh)
	newHigh.Mul(&rng, &tmp)
	newHigh.Div(&newHigh, tot)
	newHigh.Add(&newHigh, &e.low)

	tmp.SetUint64(cumLow)
	newLow.Mul(&rng, &tmp)
	newLow.Div(&newLow, tot)
	newLow.Add(&newLow, &e.low)

	e.high.Set(&newHigh)
	e.low.Set(&newLow)
	e.renormalize()
}

// renormalize shifts out bits that can no longer change, growing the
// interval back to full precision. Bits still ambiguous due to a pending
// carry are deferred with the bit-plus-follow technique.
func (e *encoder) renormalize() {
	for {
		if e.high.Cmp(half) <= 0 {
			e.emit(0)
		} else if e.low.Cmp(half) >= 0 {
			e.emit(1)
			e.low.Sub(&e.low, half)
			e.high.Sub(&e.high, half)
		} else if e.low.Cmp(quarter) >= 0 && e.high.Cmp(threeQuarter) < 0 {
			e.pending++
			e.low.Sub(&e.low, quarter)
			e.high.Sub(&e.high, quarter)
		} else {
			return
		}
		e.low.Lsh(&e.low, 1)
		e.high.Lsh(&e.high, 1)
	}
}

func (e *encoder) emit(bit int) {
	e.w.writeBit(bit)
	for ; e.pending > 0; e.pending-- {
		e.w.writeBit(bit ^ 1)
	}
}

// finish emits enough bits to pin the final interval down to a single value.
func (e *encoder) finish() []byte {
	e.pending++
	if e.low.Cmp(quarter) < 0 {
		e.emit(0)
	} else {
		e.emit(1)
	}
	return e.w.bytes()
}

// decoder mirrors the encoder interval and reads the encoded value.
type decoder struct {
	low, high, value big.Int
	r                bitReader
}

func newDecoder(data []byte) *decoder {
	d := &decoder{r: bitReader{data: data}}
	d.high.Set(full)
	for i := 0; i < precision; i++ {
		d.value.Lsh(&d.value, 1)
		if d.r.readBit() == 1 {
			d.value.SetBit(&d.value, 0, 1)
		}
	}
	return d
}

// cum maps the encoded value back to a cumulative frequency in [0, total).
func (d *decoder) cum(total uint64) uint64 {
	var rng, num big.Int
	rng.Sub(&d.high, &d.low)
	num.Sub(&d.value, &d.low)
	num.Mul(&num, new(big.Int).SetUint64(total))
	num.Div(&num, &rng)
	return num.Uint64()
}

// update applies the same interval subdivision as the encoder.
func (d *decoder) update(cumLow, cumHigh, total uint64) {
	var rng, newHigh, newLow, tmp big.Int
	rng.Sub(&d.high, &d.low)
	tot := new(big.Int).SetUint64(total)

	tmp.SetUint64(cumHigh)
	newHigh.Mul(&rng, &tmp)
	newHigh.Div(&newHigh, tot)
	newHigh.Add(&newHigh, &d.low)

	tmp.SetUint64(cumLow)
	newLow.Mul(&rng, &tmp)
	newLow.Div(&newLow, tot)
	newLow.Add(&newLow, &d.low)

	d.high.Set(&newHigh)
	d.low.Set(&newLow)
	d.renormalize()
}

func (d *decoder) renormalize() {
	for {
		if d.high.Cmp(half) <= 0 {
			// nothing to subtract
		} else if d.low.Cmp(half) >= 0 {
			d.low.Sub(&d.low, half)
			d.high.Sub(&d.high, half)
			d.value.Sub(&d.value, half)
		} else if d.low.Cmp(quarter) >= 0 && d.high.Cmp(threeQuarter) < 0 {
			d.low.Sub(&d.low, quarter)
			d.high.Sub(&d.high, quarter)
			d.value.Sub(&d.value, quarter)
		} else {
			return
		}
		d.low.Lsh(&d.low, 1)
		d.high.Lsh(&d.high, 1)
		d.value.Lsh(&d.value, 1)
		if d.r.readBit() == 1 {
			d.value.SetBit(&d.value, 0, 1)
		}
	}
}

// Model describes an adaptive probability distribution over symbols.
// Encoder and decoder must apply identical updates to stay synchronized.
type Model interface {
	// Total returns the sum of all symbol frequencies.
	Total() uint64
	// CumRange returns the cumulative frequency range of sym.
	CumRange(sym int) (cumLow, cumHigh uint64)
	// Find returns the symbol whose cumulative range contains cum.
	Find(cum uint64) (sym int, cumLow, cumHigh uint64)
	// Update adapts the model after encoding/decoding sym.
	Update(sym int)
}

// Encode compresses symbols with the given model. The output is a self
// delimiting stream: an 8 byte big endian symbol count followed by the
// arithmetic coded bit stream.
func Encode(symbols []int, model Model) []byte {
	enc := newEncoder()
	for _, sym := range symbols {
		cumLow, cumHigh := model.CumRange(sym)
		enc.encode(cumLow, cumHigh, model.Total())
		model.Update(sym)
	}
	payload := enc.finish()
	out := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint64(out, uint64(len(symbols)))
	return append(out, payload...)
}

// Decode decompresses a stream produced by Encode, resolving each symbol
// from the cumulative distribution, and stops exactly after the stored
// symbol count.
func Decode(stream []byte, model Model) []int {
	n := binary.BigEndian.Uint64(stream[:8])
	dec := newDecoder(stream[8:])
	symbols := make([]int, 0, n)
	for i := uint64(0); i < n; i++ {
		total := model.Total()
		sym, cumLow, cumHigh := model.Find(dec.cum(total))
		dec.update(cumLow, cumHigh, total)
		model.Update(sym)
		symbols = append(symbols, sym)
	}
	return symbols
}
