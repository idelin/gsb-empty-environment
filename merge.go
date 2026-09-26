package efano

import "fmt"

// cursor walks one encoded sequence through its high/low representation
// only: the high vector is consumed bit by bit (zeros advance the high
// part, ones emit a value) and the low vector is read at the matching
// packed offset. Nothing is fully decoded; extra memory is O(1).
type cursor struct {
	ef   *EliasFano
	pos  uint64 // current bit position in the high vector
	high uint64 // zeros seen so far == current high part
	idx  uint64 // values emitted so far
}

func (c *cursor) next() (hi, lo uint64, ok bool) {
	if c.idx >= c.ef.n {
		return 0, 0, false
	}
	for c.ef.high.get(c.pos) == 0 {
		c.pos++
		c.high++
	}
	c.pos++ // consume the one bit for this value
	hi = c.high
	lo = c.ef.low.getBits(c.idx*uint64(c.ef.l), c.ef.l)
	c.idx++
	return hi, lo, true
}

// Merge combines two encoded sequences into one encoded sequence,
// reading only the high/low bit streams of the inputs (no full decode
// into intermediate arrays) and writing the merged unary gaps and
// packed low parts directly. Both inputs must share the same low-bit
// width; duplicates across inputs are preserved (a's copy first).
// Extra memory beyond the output is O(1).
func Merge(a, b *EliasFano) (*EliasFano, error) {
	if a.l != b.l {
		return nil, fmt.Errorf("efano: merge needs equal low widths (%d != %d)", a.l, b.l)
	}
	out := NewBuilder(a.l)
	ca, cb := cursor{ef: a}, cursor{ef: b}
	ha, la, oka := ca.next()
	hb, lb, okb := cb.next()
	for oka || okb {
		var h, lo uint64
		if !okb || (oka && (ha < hb || (ha == hb && la <= lb))) {
			h, lo = ha, la
			ha, la, oka = ca.next()
		} else {
			h, lo = hb, lb
			hb, lb, okb = cb.next()
		}
		if err := out.addParts(h, lo); err != nil {
			return nil, err
		}
	}
	return out.Build(), nil
}
