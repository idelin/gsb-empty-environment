package eliasfano

import "fmt"

// Builder streams a non-decreasing sequence into Elias-Fano form.
// Memory use is bounded by the compressed output size (n*(l+3) bits),
// independent of the universe: values are appended one at a time and
// the high/low bitvectors grow incrementally.
type Builder struct {
	n          uint64
	universe   uint64
	lowBits    uint64
	numBuckets uint64
	low        []uint64
	high       []uint64
	count      uint64
	prev       uint64
	hasPrev    bool
}

// NewBuilder prepares a stream for exactly n values in [0, universe).
func NewBuilder(n, universe uint64) *Builder {
	l := lowBitsFor(n, universe)
	return &Builder{
		n:          n,
		universe:   universe,
		lowBits:    l,
		numBuckets: numBucketsFor(n, universe, l),
	}
}

// Add appends the next value. Values must arrive in non-decreasing
// order (duplicates allowed) and be < universe.
func (b *Builder) Add(v uint64) error {
	if b.count >= b.n {
		return fmt.Errorf("eliasfano: builder already holds %d values", b.n)
	}
	if v >= b.universe {
		return fmt.Errorf("eliasfano: value %d outside universe [0,%d)", v, b.universe)
	}
	if b.hasPrev && v < b.prev {
		return fmt.Errorf("eliasfano: value %d breaks non-decreasing order (prev %d)", v, b.prev)
	}
	// High part: the i-th value with high part h sets bit h+i.
	pos := (v >> b.lowBits) + b.count
	wi, off := pos/64, pos%64
	for uint64(len(b.high)) <= wi {
		b.high = append(b.high, 0)
	}
	b.high[wi] |= 1 << off
	// Low part: append l bits at absolute position count*l.
	if b.lowBits > 0 {
		b.setLow(b.count*b.lowBits, v&(uint64(1)<<b.lowBits-1))
	}
	b.prev = v
	b.hasPrev = true
	b.count++
	return nil
}

func (b *Builder) setLow(pos, val uint64) {
	wi, off := pos/64, pos%64
	for uint64(len(b.low)) <= wi {
		b.low = append(b.low, 0)
	}
	b.low[wi] |= val << off
	if off+b.lowBits > 64 {
		for uint64(len(b.low)) <= wi+1 {
			b.low = append(b.low, 0)
		}
		b.low[wi+1] |= val >> (64 - off)
	}
}

// Build finalizes the sequence. It fails unless exactly n values were
// added, so a truncated stream never produces a silently short index.
func (b *Builder) Build() (*EliasFano, error) {
	if b.count != b.n {
		return nil, fmt.Errorf("eliasfano: stream ended after %d of %d values", b.count, b.n)
	}
	// Normalize slice lengths to the exact format sizes; the incremental
	// appends above may stop short of trailing all-zero words.
	highBits := b.n + b.numBuckets
	for uint64(len(b.high)) < ceilDiv64(highBits) {
		b.high = append(b.high, 0)
	}
	for uint64(len(b.low)) < ceilDiv64(b.n*b.lowBits) {
		b.low = append(b.low, 0)
	}
	return &EliasFano{
		n:          b.n,
		universe:   b.universe,
		lowBits:    b.lowBits,
		numBuckets: b.numBuckets,
		highBits:   highBits,
		low:        b.low,
		high:       b.high,
	}, nil
}

// MustBuild is Build for test/demo code that knows the stream is exact.
func (b *Builder) MustBuild() *EliasFano {
	ef, err := b.Build()
	if err != nil {
		panic(err)
	}
	return ef
}
