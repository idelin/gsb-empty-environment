package bitlayout

// Cost describes the execution cost of a plan, derived from the schema
// size and the actual record size. All fields are upper bounds unless
// noted. The model is:
//
//	encode time   = O(F + B/8 + C)      F = field count, B = record bits,
//	                                  C = checksum-covered bits
//	decode time   = O(F + B/8 + C)
//	decode memory = O(B/8 + E)          E = total elements (session input
//	                                  buffer + decoded value tree)
//	compile time  = O(S)                S = schema node count
//
// Per-bit work is constant: each stream bit is written/read exactly once
// (plus once more for checksummed ranges), in byte-sized chunks where
// possible.
type Cost struct {
	Fields        int   // total schema nodes
	MaxDepth      int   // deepest nesting in the schema
	MinRecordBits int64 // smallest possible record
	MaxRecordBits int64 // largest possible record
	MaxElements   uint64
	// ChecksumBits is the total number of bits read a second time for
	// checksum verification (worst case).
	ChecksumBits int64
}

// Cost derives the static cost model of the plan.
func (p *Plan) Cost() Cost {
	c := Cost{MinRecordBits: p.MinBits, MaxRecordBits: p.MaxBits}
	var walk func(n *node, depth int)
	walk = func(n *node, depth int) {
		if n == nil {
			return
		}
		c.Fields++
		if depth > c.MaxDepth {
			c.MaxDepth = depth
		}
		if n.kind == kindArray {
			hi, lo := add64(c.MaxElements, n.maxElems)
			if hi == 0 {
				c.MaxElements = lo
			} else {
				c.MaxElements = ^uint64(0)
			}
		}
		if n.kind == kindChecksum {
			for i := n.fromIx; i <= n.toIx; i++ {
				c.ChecksumBits += p.root.fields[i].maxBits
			}
		}
		for _, f := range n.fields {
			walk(f, depth+1)
		}
		walk(n.elem, depth+1)
		for _, cs := range n.cases {
			walk(cs, depth+1)
		}
	}
	walk(p.root, 0)
	return c
}

func add64(a, b uint64) (hi, lo uint64) {
	lo = a + b
	if lo < a {
		hi = 1
	}
	return
}
