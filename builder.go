package efano

import "fmt"

// Builder streams a non-decreasing sequence into Elias-Fano form with
// O(1) state beyond the compressed output itself: values are consumed
// one at a time and never buffered, so peak memory is bounded by the
// encoded size (<= n*(l+3) bits for a well-chosen l), not by the raw
// input. The low width l is fixed up front (see ChooseL); the universe
// never needs to be allocated, so huge upper bounds are safe.
type Builder struct {
	l        uint
	n        uint64
	lastHigh uint64
	lastVal  uint64
	high     bitvec
	low      bitvec
}

// NewBuilder creates a streaming builder with low-bit width l (0..63).
func NewBuilder(l uint) *Builder {
	if l > 63 {
		panic("efano: low-bit width must be <= 63")
	}
	return &Builder{l: l}
}

// Add appends v, which must be >= every previously added value.
func (b *Builder) Add(v uint64) error {
	if b.n > 0 && v < b.lastVal {
		return fmt.Errorf("efano: non-monotone input: %d after %d", v, b.lastVal)
	}
	return b.addParts(v>>b.l, v&mask64(b.l))
}

// addParts appends one value given its split high/low parts.
func (b *Builder) addParts(hi, lo uint64) error {
	if b.n > 0 {
		v := hi<<b.l | lo
		if v < b.lastVal {
			return fmt.Errorf("efano: non-monotone parts: %d after %d", v, b.lastVal)
		}
		b.lastVal = v
	} else {
		b.lastVal = hi<<b.l | lo
	}
	b.high.appendZeros(hi - b.lastHigh)
	b.high.appendBit(1)
	b.low.appendBits(lo, b.l)
	b.lastHigh = hi
	b.n++
	return nil
}

// Build seals the index and constructs the rank directory.
func (b *Builder) Build() *EliasFano {
	ef := &EliasFano{
		n:      b.n,
		l:      b.l,
		maxVal: b.lastVal,
		high:   b.high,
		low:    b.low,
	}
	ef.buildRank()
	return ef
}
