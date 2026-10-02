package tans

// This file implements a classic carry-less range coder (Subbotin-style)
// as a representative traditional arithmetic coder. It is used as a
// correctness cross-check and as the throughput baseline that the tANS
// benchmarks are compared against.

const (
	rcTop = 1 << 24
	rcBot = 1 << 16
)

// rangeModel is a static cumulative-frequency model for the range coder.
type rangeModel struct {
	cumul [AlphabetSize + 1]uint32
	freq  [AlphabetSize]uint32
	total uint32
}

func newRangeModel(norm [AlphabetSize]uint16) *rangeModel {
	m := &rangeModel{}
	for s := 0; s < AlphabetSize; s++ {
		m.freq[s] = uint32(norm[s])
		m.cumul[s+1] = m.cumul[s] + m.freq[s]
	}
	m.total = m.cumul[AlphabetSize]
	return m
}

type rangeEncoder struct {
	low, rng uint32
	buf      []byte
}

func (e *rangeEncoder) encodeSymbol(m *rangeModel, s byte) {
	r := e.rng / m.total
	e.low += m.cumul[s] * r
	e.rng = r * m.freq[s]
	for {
		if (e.low ^ (e.low + e.rng)) < rcTop {
			// Same top byte: shift it out.
		} else if e.rng < rcBot {
			// Range too small: squeeze it away from the carry boundary.
			e.rng = -e.low & (rcBot - 1)
		} else {
			break
		}
		e.buf = append(e.buf, byte(e.low>>24))
		e.low <<= 8
		e.rng <<= 8
	}
}

func (e *rangeEncoder) close() {
	for i := 0; i < 4; i++ {
		e.buf = append(e.buf, byte(e.low>>24))
		e.low <<= 8
	}
}

type rangeDecoder struct {
	low, rng, code uint32
	buf            []byte
	pos            int
}

func (d *rangeDecoder) nextByte() uint32 {
	if d.pos < len(d.buf) {
		b := d.buf[d.pos]
		d.pos++
		return uint32(b)
	}
	return 0
}

func (d *rangeDecoder) decodeSymbol(m *rangeModel) byte {
	r := d.rng / m.total
	count := (d.code - d.low) / r
	// Binary search for the symbol whose cumulative range contains count.
	lo, hi := 0, AlphabetSize-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if m.cumul[mid] <= count {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	s := lo
	d.low += m.cumul[s] * r
	d.rng = r * m.freq[s]
	for {
		if (d.low ^ (d.low + d.rng)) < rcTop {
		} else if d.rng < rcBot {
			d.rng = -d.low & (rcBot - 1)
		} else {
			break
		}
		d.code = (d.code << 8) | d.nextByte()
		d.low <<= 8
		d.rng <<= 8
	}
	return byte(s)
}

// rangeEncode compresses src with the range coder under the given model.
func rangeEncode(m *rangeModel, src []byte) []byte {
	e := &rangeEncoder{rng: 0xFFFFFFFF}
	for _, s := range src {
		e.encodeSymbol(m, s)
	}
	e.close()
	return e.buf
}

// rangeDecode decompresses exactly n symbols from a range-coded stream.
func rangeDecode(m *rangeModel, src []byte, n int) []byte {
	d := &rangeDecoder{rng: 0xFFFFFFFF, buf: src}
	for i := 0; i < 4; i++ {
		d.code = (d.code << 8) | d.nextByte()
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = d.decodeSymbol(m)
	}
	return out
}

// RangeBaseline is a traditional arithmetic coder (carry-less range coder)
// built on the same normalized frequency model as a tANS Table. It exists
// as a comparison point: tANS matches its compression ratio while running
// several times faster (see the benchmarks and cmd/tans-demo).
type RangeBaseline struct {
	m *rangeModel
}

// NewRangeBaseline returns a range coder sharing the frequency model of t.
func NewRangeBaseline(t *Table) *RangeBaseline {
	return &RangeBaseline{m: newRangeModel(t.norm)}
}

// Encode compresses src with the range coder.
func (r *RangeBaseline) Encode(src []byte) []byte {
	return rangeEncode(r.m, src)
}

// Decode decompresses exactly n symbols from a range-coded stream.
func (r *RangeBaseline) Decode(src []byte, n int) []byte {
	return rangeDecode(r.m, src, n)
}
