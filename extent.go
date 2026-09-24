package blkmap

// Extent maps the logical range [Off, Off+Len) to the immutable physical
// block Block at offset BOff. Extents are kept sorted by Off and never
// overlap; logical ranges not covered by any extent are holes and read
// back as zeros without occupying a physical block.
type Extent struct {
	Off   uint64 `json:"off"`
	Len   uint64 `json:"len"`
	Block uint64 `json:"block"`
	BOff  uint64 `json:"boff"`
}

// replaceRange removes every part of exts overlapping [off, off+length),
// inserts repl (which must lie inside that range, sorted), and returns the
// resulting sorted, non-overlapping, merged extent list.
func replaceRange(exts []Extent, off, length uint64, repl []Extent) []Extent {
	end := off + length
	out := make([]Extent, 0, len(exts)+len(repl))
	inserted := false
	for _, e := range exts {
		eEnd := e.Off + e.Len
		switch {
		case eEnd <= off:
			out = append(out, e)
		case e.Off >= end:
			if !inserted {
				out = append(out, repl...)
				inserted = true
			}
			out = append(out, e)
		default:
			if e.Off < off { // keep the left surviving fragment
				out = append(out, Extent{Off: e.Off, Len: off - e.Off, Block: e.Block, BOff: e.BOff})
			}
			if !inserted {
				out = append(out, repl...)
				inserted = true
			}
			if eEnd > end { // keep the right surviving fragment
				out = append(out, Extent{Off: end, Len: eEnd - end, Block: e.Block, BOff: e.BOff + end - e.Off})
			}
		}
	}
	if !inserted {
		out = append(out, repl...)
	}
	return mergeAdjacent(out)
}

// mergeAdjacent coalesces neighbouring extents that reference contiguous
// regions of the same physical block.
func mergeAdjacent(exts []Extent) []Extent {
	if len(exts) == 0 {
		return exts
	}
	out := exts[:1]
	for _, e := range exts[1:] {
		last := &out[len(out)-1]
		if last.Off+last.Len == e.Off && last.Block == e.Block && last.BOff+last.Len == e.BOff {
			last.Len += e.Len
			continue
		}
		out = append(out, e)
	}
	return out
}
