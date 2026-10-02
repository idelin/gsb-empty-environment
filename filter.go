package dictcol

import "encoding/binary"

const (
	swarOnes  = 0x0101010101010101
	swarLow7  = 0x7f7f7f7f7f7f7f7f
	swarHighs = 0x8080808080808080
)

// hasZeroByte returns a word with the high bit set in each byte lane of x
// that equals zero. Uses the carry-free exact form: per byte b,
// (b&0x7f)+0x7f never carries into the next lane, and its high bit is set
// iff b != 0 (for b < 0x80) — so the inverted high bit marks exact zeros.
func hasZeroByte(x uint64) uint64 {
	return ^(((x & swarLow7) + swarLow7) | x) & swarHighs
}

// eqByte returns a word with the high bit set in each byte lane equal to v.
func eqByte(word uint64, v byte) uint64 {
	return hasZeroByte(word ^ (swarOnes * uint64(v)))
}

// Mask is a row-selection bitmask: bit i set means row i matched.
type Mask []byte

// Set marks row i as matched.
func (m Mask) Set(i int) { m[i>>3] |= 1 << uint(i&7) }

// Get reports whether row i is matched.
func (m Mask) Get(i int) bool { return m[i>>3]>>(uint(i)&7)&1 == 1 }

// Count returns the number of matched rows.
func (m Mask) Count() int {
	n := 0
	for _, b := range m {
		for b != 0 {
			b &= b - 1
			n++
		}
	}
	return n
}

// FilterIn pushes the predicate `col IN (values...)` down to the dictionary:
// each string is interned once to an integer ID, then the encoded byte array
// is scanned word-at-a-time (8 rows per 64-bit SWAR step, no per-row string
// comparison). The result is a bitmask over row indices.
func (c *Column) FilterIn(values ...string) Mask {
	// Dictionary pushdown: strings -> integer IDs, done once per query.
	ids := make([]byte, 0, len(values))
	for _, v := range values {
		if id := c.Dict.Intern(v); id >= 0 {
			ids = append(ids, byte(id))
		}
	}

	n := len(c.Codes)
	mask := make(Mask, (n+7)/8)
	if len(ids) == 0 {
		return mask
	}

	// Vectorized main loop: 8 rows per iteration on the raw byte array.
	i := 0
	for ; i+8 <= n; i += 8 {
		w := binary.LittleEndian.Uint64(c.Codes[i:])
		var hit uint64
		for _, id := range ids {
			hit |= eqByte(w, id)
		}
		if hit == 0 {
			continue
		}
		// Collapse the per-lane high bits into an 8-bit row mask.
		var bits byte
		for lane := 0; lane < 8; lane++ {
			bits |= byte((hit>>uint(8*lane+7))&1) << uint(lane)
		}
		mask[i>>3] = bits
	}
	// Scalar tail for the remaining <8 rows.
	for ; i < n; i++ {
		code := c.Codes[i]
		for _, id := range ids {
			if code == id {
				mask.Set(i)
				break
			}
		}
	}
	return mask
}
