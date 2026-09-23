package huffcodec

// levelBits is the fan-out of each decode table level. Codes longer than
// levelBits spill into chained sub-tables, so the table is layered and
// memory stays proportional to the code distribution rather than 2^maxLen.
const levelBits = 8

type tabEntry struct {
	sym    uint16     // valid when term is set
	length uint8      // remaining code length at this level, when term
	term   bool       // entry decodes a symbol directly
	sub    *decodeTab // non-nil when the prefix continues in a sub-table
}

type decodeTab struct {
	entries []tabEntry // always 1<<levelBits entries
}

type codeItem struct {
	suffix uint32 // remaining code bits, right-aligned
	rem    uint8  // remaining code length in bits
	sym    int
}

// buildDecodeTable builds the layered decode table from canonical codes.
// Conflicting entries indicate an oversubscribed table and are rejected.
// Holes (incomplete tables) are tolerated here; ValidateLengths enforces
// completeness policy before this is called.
func buildDecodeTable(codes []Code) (*decodeTab, error) {
	var items []codeItem
	for sym, c := range codes {
		if c.Len == 0 {
			continue
		}
		items = append(items, codeItem{suffix: c.Bits, rem: c.Len, sym: sym})
	}
	return buildLevel(items)
}

func buildLevel(items []codeItem) (*decodeTab, error) {
	t := &decodeTab{entries: make([]tabEntry, 1<<levelBits)}
	subGroups := make(map[uint32][]codeItem)
	var order []uint32
	for _, it := range items {
		if int(it.rem) <= levelBits {
			idx := it.suffix << (levelBits - it.rem)
			span := uint32(1) << (levelBits - it.rem)
			for j := idx; j < idx+span; j++ {
				if t.entries[j].term || t.entries[j].sub != nil {
					return nil, ErrOversubscribed
				}
				t.entries[j] = tabEntry{sym: uint16(it.sym), length: it.rem, term: true}
			}
			continue
		}
		prefix := it.suffix >> (it.rem - levelBits)
		if _, ok := subGroups[prefix]; !ok {
			order = append(order, prefix)
		}
		subGroups[prefix] = append(subGroups[prefix], codeItem{
			suffix: it.suffix & ((1 << (it.rem - levelBits)) - 1),
			rem:    it.rem - levelBits,
			sym:    it.sym,
		})
	}
	for _, p := range order {
		if t.entries[p].term {
			return nil, ErrOversubscribed
		}
		sub, err := buildLevel(subGroups[p])
		if err != nil {
			return nil, err
		}
		t.entries[p].sub = sub
	}
	return t, nil
}
