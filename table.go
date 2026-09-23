package prefixcode

import "sort"

// levelBits is the number of bits consumed per lookup level.
const levelBits = 8

// entry is one slot of a decode-table level. A direct entry (sym >= 0)
// emits sym and consumes consume bits. A link entry (sub >= 0) consumes
// levelBits bits and continues in subs[sub]. An entry with sym < 0 and
// sub < 0 matches no codeword.
type entry struct {
	sym     int32
	consume uint8
	sub     int32
}

// Table is a layered canonical-code decoding table. The root level is
// indexed by the first 8 bits of the stream; codes longer than 8 bits
// chain into 256-entry subtables, one level per additional 8-bit slice,
// so memory stays proportional to the alphabet size rather than 2^maxLen.
type Table struct {
	// MaxLen is the longest code length in the table.
	MaxLen int
	// Complete reports whether the Kraft sum of the lengths is exactly 1.
	Complete bool

	root []entry
	subs [][]entry
}

type codeInfo struct {
	sym    int
	code   uint32
	length int
}

// NewTable builds a decoding table from code lengths. Oversubscribed
// lengths (Kraft sum > 1) are rejected; incomplete tables are allowed and
// simply make some bit patterns undecodable.
func NewTable(lengths []int) (*Table, error) {
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		return nil, err
	}
	t := &Table{}
	var list []codeInfo
	for sym, c := range codes {
		if c.Len == 0 {
			continue
		}
		list = append(list, codeInfo{sym: sym, code: c.Bits, length: int(c.Len)})
		if int(c.Len) > t.MaxLen {
			t.MaxLen = int(c.Len)
		}
	}
	if t.MaxLen > 0 {
		t.Complete = Validate(lengths, t.MaxLen) == nil
	}
	t.root = t.build(list, 0)
	return t, nil
}

// build creates one 256-entry level for codes, where off bits of each code
// have already been consumed by parent levels.
func (t *Table) build(list []codeInfo, off int) []entry {
	entries := make([]entry, 1<<levelBits)
	for i := range entries {
		entries[i].sym = -1
		entries[i].sub = -1
	}
	groups := make(map[uint32][]codeInfo)
	var order []uint32
	for _, c := range list {
		rem := c.length - off
		if rem <= levelBits {
			base := (c.code & mask32(rem)) << uint(levelBits-rem)
			for k := uint32(0); k < uint32(1)<<uint(levelBits-rem); k++ {
				entries[base+k] = entry{sym: int32(c.sym), consume: uint8(rem), sub: -1}
			}
			continue
		}
		p := (c.code >> uint(rem-levelBits)) & 0xff
		if _, ok := groups[p]; !ok {
			order = append(order, p)
		}
		groups[p] = append(groups[p], c)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	for _, p := range order {
		sub := t.build(groups[p], off+levelBits)
		entries[p].sub = int32(len(t.subs))
		t.subs = append(t.subs, sub)
	}
	return entries
}

func mask32(n int) uint32 {
	if n >= 32 {
		return ^uint32(0)
	}
	return (uint32(1) << uint(n)) - 1
}

func mask64(n int) uint64 {
	if n >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << uint(n)) - 1
}
