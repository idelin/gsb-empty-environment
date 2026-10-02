package dictenc

import "encoding/binary"

// Mask is a selection vector: 1 keeps the row, 0 drops it.
type Mask []byte

// All returns a mask selecting every row of n.
func All(n int) Mask {
	m := make(Mask, n)
	for i := range m {
		m[i] = 1
	}
	return m
}

// Count returns the number of selected rows.
func (m Mask) Count() int {
	n := 0
	for _, b := range m {
		n += int(b)
	}
	return n
}

// Op is a comparison operator evaluated vectorized over dictionary IDs.
type Op int

const (
	EQ Op = iota
	NE
	LT
	LE
	GT
	GE
)

// Compare filters the column with `col <op> literal`. The string literal is
// pushed down through the dictionary exactly once (dictionary transform
// pushdown); the hot loop then compares raw integer IDs only.
//
// Ordering operators compare dictionary IDs, which is meaningful when the
// dictionary preserves value order (as NewDictionary does for sorted input).
func (c *Column) Compare(op Op, literal string) Mask {
	id, ok := c.Dict.Lookup(literal)
	if !ok {
		// Literal absent from the dictionary: EQ matches nothing,
		// NE matches everything, ordering falls back to ID semantics.
		switch op {
		case EQ:
			return make(Mask, len(c.IDs))
		case NE:
			return All(len(c.IDs))
		}
	}
	return compareID(c.IDs, op, byte(id))
}

// compareID is the vectorized integer comparison kernel over the ID vector.
func compareID(ids []byte, op Op, target byte) Mask {
	out := make(Mask, len(ids))
	switch op {
	case EQ:
		eqSWAR(ids, target, out)
	case NE:
		eqSWAR(ids, target, out)
		for i := range out {
			out[i] ^= 1
		}
	case LT:
		for i, v := range ids {
			if v < target {
				out[i] = 1
			}
		}
	case LE:
		for i, v := range ids {
			if v <= target {
				out[i] = 1
			}
		}
	case GT:
		for i, v := range ids {
			if v > target {
				out[i] = 1
			}
		}
	case GE:
		for i, v := range ids {
			if v >= target {
				out[i] = 1
			}
		}
	}
	return out
}

// eqSWAR writes 1 to out[i] where ids[i] == target, processing 8 rows per
// iteration with SWAR (SIMD Within A Register) word tricks: one 64-bit XOR
// plus two arithmetic ops tests eight bytes in a single cycle group.
func eqSWAR(ids []byte, target byte, out Mask) {
	const (
		ones  = 0x0101010101010101
		lows  = 0x7f7f7f7f7f7f7f7f
		highs = 0x8080808080808080
	)
	broadcast := uint64(target) * ones

	i := 0
	for ; i+8 <= len(ids); i += 8 {
		word := binary.LittleEndian.Uint64(ids[i:])
		x := word ^ broadcast
		// Exact zero-byte detection: a byte of x is 0 iff its high bit is
		// set in hit. (The classic (x-ones)&^x variant is not exact: it
		// false-positives on 0x01 bytes after a borrow from a zero byte.)
		hit := ^((x & lows) + lows | x) & highs
		out[i+0] = byte(hit >> 7)
		out[i+1] = byte(hit >> 15)
		out[i+2] = byte(hit >> 23)
		out[i+3] = byte(hit >> 31)
		out[i+4] = byte(hit >> 39)
		out[i+5] = byte(hit >> 47)
		out[i+6] = byte(hit >> 55)
		out[i+7] = byte(hit >> 63)
	}
	for ; i < len(ids); i++ {
		if ids[i] == target {
			out[i] = 1
		}
	}
}

// In filters the column with `col IN (literals...)`. Every literal is pushed
// down through the dictionary once, producing a 256-entry integer lookup
// table; the scan over the ID vector is then a single indexed load per row.
func (c *Column) In(literals ...string) Mask {
	var table [256]byte
	for _, lit := range literals {
		if id, ok := c.Dict.Lookup(lit); ok {
			table[byte(id)] = 1
		}
	}
	return inTable(c.IDs, &table)
}

// inTable is the vectorized IN kernel over the ID vector.
func inTable(ids []byte, table *[256]byte) Mask {
	out := make(Mask, len(ids))
	for i, v := range ids {
		out[i] = table[v]
	}
	return out
}
