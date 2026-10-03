package tans

// This file contains a reference implementation of traditional bit-oriented
// arithmetic coding (Witten-Neal-Cleary, 32-bit code values with underflow
// bits). It is NOT part of the tANS pipeline; it exists as the baseline for
// the compression-ratio and throughput comparisons in the tests, benchmarks
// and demo program.

const (
	acFirstQtr = uint64(1) << 30 // 1/4 of the 32-bit code range
	acHalf     = 2 * acFirstQtr
	acThirdQtr = 3 * acFirstQtr
	acTop      = uint64(1)<<32 - 1
)

// fwBitWriter appends bits MSB-first; the reader consumes them in the same
// (forward) order, matching classic arithmetic coder I/O.
type fwBitWriter struct {
	buf   []byte
	acc   byte
	nbits uint
}

func (w *fwBitWriter) bit(b uint) {
	w.acc = w.acc<<1 | byte(b)
	w.nbits++
	if w.nbits == 8 {
		w.buf = append(w.buf, w.acc)
		w.acc = 0
		w.nbits = 0
	}
}

func (w *fwBitWriter) flush() {
	if w.nbits > 0 {
		w.buf = append(w.buf, w.acc<<(8-w.nbits))
		w.acc = 0
		w.nbits = 0
	}
}

type fwBitReader struct {
	buf    []byte
	pos    int
	bitPos uint
}

func (r *fwBitReader) bit() uint {
	if r.pos >= len(r.buf) {
		return 0 // implicit zero padding past the end
	}
	b := uint(r.buf[r.pos]>>(7-r.bitPos)) & 1
	r.bitPos++
	if r.bitPos == 8 {
		r.bitPos = 0
		r.pos++
	}
	return b
}

type arithEncoder struct {
	low, high uint64
	pending   int
	w         fwBitWriter
}

func (e *arithEncoder) emit(b uint) {
	e.w.bit(b)
	for ; e.pending > 0; e.pending-- {
		e.w.bit(b ^ 1)
	}
}

func (e *arithEncoder) encode(cumLow, cumHigh, total uint64) {
	r := (e.high - e.low + 1) / total
	e.high = e.low + r*cumHigh - 1
	e.low = e.low + r*cumLow
	for {
		if e.high < acHalf {
			e.emit(0)
		} else if e.low >= acHalf {
			e.emit(1)
			e.low -= acHalf
			e.high -= acHalf
		} else if e.low >= acFirstQtr && e.high < acThirdQtr {
			e.pending++
			e.low -= acFirstQtr
			e.high -= acFirstQtr
		} else {
			break
		}
		e.low <<= 1
		e.high = e.high<<1 | 1
	}
}

func (e *arithEncoder) finish() []byte {
	e.pending++
	if e.low < acFirstQtr {
		e.emit(0)
	} else {
		e.emit(1)
	}
	e.w.flush()
	return e.w.buf
}

type arithDecoder struct {
	low, high, value uint64
	r                fwBitReader
}

func (d *arithDecoder) init(data []byte) {
	d.r = fwBitReader{buf: data}
	d.high = acTop
	for i := 0; i < 32; i++ {
		d.value = d.value<<1 | uint64(d.r.bit())
	}
}

func (d *arithDecoder) decode(cum *[257]uint64) byte {
	total := cum[256]
	r := (d.high - d.low + 1) / total
	cv := (d.value - d.low) / r
	if cv >= total {
		cv = total - 1
	}
	// Find s with cum[s] <= cv < cum[s+1].
	lo, hi := 0, 256
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if cum[mid] <= cv {
			lo = mid
		} else {
			hi = mid
		}
	}
	s := lo
	d.high = d.low + r*cum[s+1] - 1
	d.low = d.low + r*cum[s]
	for {
		if d.high < acHalf {
			// Nothing to subtract.
		} else if d.low >= acHalf {
			d.low -= acHalf
			d.high -= acHalf
			d.value -= acHalf
		} else if d.low >= acFirstQtr && d.high < acThirdQtr {
			d.low -= acFirstQtr
			d.high -= acFirstQtr
			d.value -= acFirstQtr
		} else {
			break
		}
		d.low <<= 1
		d.high = d.high<<1 | 1
		d.value = d.value<<1 | uint64(d.r.bit())
	}
	return byte(s)
}

func cumFreq(freq *[256]uint64) [257]uint64 {
	var cum [257]uint64
	for s := 0; s < 256; s++ {
		cum[s+1] = cum[s] + freq[s]
	}
	return cum
}

// ArithCompress encodes src with the reference arithmetic coder using a
// static order-0 model built from freq. Baseline for comparison only.
func ArithCompress(src []byte, freq *[256]uint64) []byte {
	cum := cumFreq(freq)
	e := &arithEncoder{high: acTop}
	for _, b := range src {
		e.encode(cum[b], cum[int(b)+1], cum[256])
	}
	return e.finish()
}

// ArithDecompress decodes length symbols from data produced by
// ArithCompress with the same frequency table.
func ArithDecompress(data []byte, length int, freq *[256]uint64) []byte {
	cum := cumFreq(freq)
	d := &arithDecoder{}
	d.init(data)
	out := make([]byte, length)
	for i := range out {
		out[i] = d.decode(&cum)
	}
	return out
}
