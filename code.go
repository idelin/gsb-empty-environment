package llex

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// MaxCodecL is the largest code length the canonical codec and the wire
// format support (code values are 64-bit). The length solver itself
// accepts any L >= 1.
const MaxCodecL = 64

// Code is a canonical prefix code built from a length table. Codewords are
// assigned canonically: symbols ordered by (length, ID) receive consecutive
// values, so the table of lengths fully determines the code.
type Code struct {
	Table
	enc map[string]codeword
	dec map[deckey]string
}

type codeword struct {
	bits uint64
	n    int
}

type deckey struct {
	l uint8
	v uint64
}

// BuildCode validates a length table and derives the canonical code.
// It rejects: L outside [1, MaxCodecL], lengths outside [1, L], duplicate
// or unsorted IDs, and oversubscribed tables (Kraft sum > 1). Incomplete
// tables (Kraft sum < 1) are accepted; only their defined codewords will
// decode, holes are never filled with arbitrary symbols.
func BuildCode(t *Table) (*Code, error) {
	n := len(t.IDs)
	if t.L < 1 || t.L > MaxCodecL {
		return nil, fmt.Errorf("llex: L=%d outside codec range [1,%d]", t.L, MaxCodecL)
	}
	if len(t.Lengths) != n {
		return nil, fmt.Errorf("llex: %d IDs but %d lengths", n, len(t.Lengths))
	}
	if !sort.StringsAreSorted(t.IDs) {
		return nil, errors.New("llex: symbol IDs are not in canonical sorted order")
	}
	kraft := new(big.Int)
	for i, id := range t.IDs {
		if i > 0 && t.IDs[i-1] == id {
			return nil, fmt.Errorf("llex: duplicate symbol ID %q", id)
		}
		l := t.Lengths[i]
		if l < 1 || l > t.L {
			return nil, fmt.Errorf("llex: symbol %q has length %d outside [1,%d]", id, l, t.L)
		}
		kraft.Add(kraft, new(big.Int).Lsh(big.NewInt(1), uint(t.L-l)))
	}
	if kraft.Cmp(new(big.Int).Lsh(big.NewInt(1), uint(t.L))) > 0 {
		return nil, fmt.Errorf("llex: oversubscribed code table: Kraft sum is %s/2^%d > 1", kraft.String(), t.L)
	}

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		la, lb := t.Lengths[order[a]], t.Lengths[order[b]]
		if la != lb {
			return la < lb
		}
		return order[a] < order[b]
	})

	c := &Code{
		Table: *t,
		enc:   make(map[string]codeword, n),
		dec:   make(map[deckey]string, n),
	}
	var code uint64
	prevLen := 0
	for _, idx := range order {
		l := t.Lengths[idx]
		code <<= uint(l - prevLen)
		prevLen = l
		if l < MaxCodecL && code>>uint(l) != 0 {
			return nil, errors.New("llex: oversubscribed code table: canonical assignment overflow")
		}
		c.enc[t.IDs[idx]] = codeword{bits: code, n: l}
		c.dec[deckey{l: uint8(l), v: code}] = t.IDs[idx]
		code++
	}
	return c, nil
}

// Encode returns the codeword for a symbol ID.
func (c *Code) Encode(id string) (bits uint64, n int, ok bool) {
	cw, ok := c.enc[id]
	return cw.bits, cw.n, ok
}

// lookup finds the symbol for the codeword v of length l.
func (c *Code) lookup(l int, v uint64) (string, bool) {
	id, ok := c.dec[deckey{l: uint8(l), v: v}]
	return id, ok
}

// bitWriter appends bits MSB-first; the final byte is zero-padded.
type bitWriter struct {
	buf    []byte
	cur    byte
	nbits  uint
	bitLen int
}

func (w *bitWriter) Write(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		w.cur <<= 1
		if v>>uint(i)&1 == 1 {
			w.cur |= 1
		}
		w.nbits++
		w.bitLen++
		if w.nbits == 8 {
			w.buf = append(w.buf, w.cur)
			w.cur = 0
			w.nbits = 0
		}
	}
}

// Bytes returns the written bytes, zero-padding the final partial byte.
func (w *bitWriter) Bytes() []byte {
	out := make([]byte, len(w.buf), len(w.buf)+1)
	copy(out, w.buf)
	if w.nbits > 0 {
		out = append(out, w.cur<<(8-w.nbits))
	}
	return out
}

// bitReader reads bits MSB-first up to a limit in bits.
type bitReader struct {
	data  []byte
	pos   int
	limit int
}

func (r *bitReader) bit() (uint64, bool) {
	if r.pos >= r.limit {
		return 0, false
	}
	b := uint64(r.data[r.pos/8] >> uint(7-r.pos%8) & 1)
	r.pos++
	return b, true
}
