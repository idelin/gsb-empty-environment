package handover

// Perm is a permutation of the n low-order bit positions of a page number.
// Bit k of the input value is routed to bit Perm[k] of the output value.
// Higher bits pass through unchanged. A permutation is a bijection: its entries
// must be exactly 0..n-1 with no duplicates.
//
// Perm models the page-bit swizzle performed by an I/O address window. It is
// a pure function so the component never touches hardware.
type Perm struct {
	// N is the number of routed low-order bits, in [1, PageBits].
	N uint
	// Map[k] is the destination bit position of source bit k.
	Map []uint
}

// IdentityPerm returns the identity permutation over n low-order bits.
func IdentityPerm(n uint) Perm {
	m := make([]uint, n)
	for k := range m {
		m[k] = uint(k)
	}
	return Perm{N: n, Map: m}
}

// Validate checks the permutation width and the bijection invariant.
func (p Perm) Validate() error {
	if p.N == 0 || p.N > PageBits {
		return errf("permutation width %d out of range [1,%d]", p.N, PageBits)
	}
	if len(p.Map) != int(p.N) {
		return errf("permutation map length %d does not match width %d", len(p.Map), p.N)
	}
	seen := make([]bool, p.N)
	for src, dst := range p.Map {
		if dst >= p.N {
			return errf("permutation entry %d -> %d exceeds width %d", src, dst, p.N)
		}
		if seen[dst] {
			return errf("permutation entry %d -> %d is not bijective: bit %d reused", src, dst, dst)
		}
		seen[dst] = true
	}
	return nil
}

// Equal reports whether two permutations have identical width and mapping.
func (p Perm) Equal(o Perm) bool {
	if p.N != o.N || len(p.Map) != len(o.Map) {
		return false
	}
	for k := range p.Map {
		if p.Map[k] != o.Map[k] {
			return false
		}
	}
	return true
}

// Apply routes the n low-order bits of v through the permutation. Bits >= n
// are preserved.
func (p Perm) Apply(v uint64) uint64 {
	var out uint64
	for src, dst := range p.Map {
		if v&(uint64(1)<<uint(src)) != 0 {
			out |= uint64(1) << dst
		}
	}
	return out | (v &^ ((uint64(1) << p.N) - 1))
}

// Inverse returns the reverse permutation.
func (p Perm) Inverse() Perm {
	inv := make([]uint, p.N)
	for src, dst := range p.Map {
		inv[dst] = uint(src)
	}
	return Perm{N: p.N, Map: inv}
}

// Image maps a normalized set of n-bit values through p. The input set must be
// contained in [0, 2^n); the output is a normalized IntervalSet. Because p is
// a bijection, the result always contains exactly as many pages as the input.
//
// The mapping is structural. The whole permutation domain maps onto itself in
// O(1); smaller ranges are decomposed into aligned power-of-two cylinders and
// built by deciding destination bits from the top down, merging adjacent
// leaves into intervals. No array indexed by the 48-bit space is allocated, so
// the full page-number space is never expanded page by page.
func (p Perm) Image(in IntervalSet) (IntervalSet, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	limit := uint64(1) << p.N
	for _, iv := range in {
		if iv.End > limit {
			return nil, errf("interval [%d,%d) exceeds permutation domain 2^%d", iv.Start, iv.End, p.N)
		}
	}
	var out IntervalSet
	for _, iv := range in {
		if iv.Start == 0 && iv.End == limit {
			// Bijection: the entire domain maps onto itself, always one range.
			out = Union(out, IntervalSet{{0, limit}})
			continue
		}
		for _, blk := range alignedBlocks(iv, p.N) {
			out = Union(out, p.cylinderImage(blk.base, blk.bits))
		}
	}
	return out, nil
}

// alignedBlock is a range [base, base+2^bits) aligned to 2^bits.
type alignedBlock struct {
	base uint64
	bits uint
}

// alignedBlocks decomposes iv into aligned power-of-two blocks within an
// n-bit domain.
func alignedBlocks(iv Interval, n uint) []alignedBlock {
	var blocks []alignedBlock
	cursor := iv.Start
	for cursor < iv.End {
		bits := n
		if cursor != 0 {
			if tz := trailingZeroBits(cursor); tz < bits {
				bits = tz
			}
		}
		for uint64(1)<<bits > iv.End-cursor {
			bits--
		}
		blocks = append(blocks, alignedBlock{base: cursor, bits: bits})
		cursor += uint64(1) << bits
	}
	return blocks
}

// cylinderImage maps the aligned block base + x for x in [0, 2^bits).
//
// Destination bits are decided from most significant to least. A destination
// position fed by a fixed source bit (source >= bits) takes the single value
// already present in Apply(base); a position fed by a free source bit takes
// both values. Leaves are emitted as unit intervals and adjacent leaves merge,
// so contiguous output runs stay compact and pages are never enumerated into
// a domain-sized array.
func (p Perm) cylinderImage(base uint64, bits uint) IntervalSet {
	fixed := p.Apply(base)
	inv := p.Inverse()
	var leaves IntervalSet
	var build func(dstCount uint, value uint64)
	build = func(dstCount uint, value uint64) {
		if dstCount == 0 {
			leaves = append(leaves, Interval{value, value + 1})
			return
		}
		dst := dstCount - 1
		src := inv.Map[dst]
		if src >= bits {
			build(dstCount-1, value)
			return
		}
		mask := uint64(1) << dst
		build(dstCount-1, value)
		build(dstCount-1, value|mask)
	}
	build(p.N, fixed)
	return normalizeOwned(leaves)
}

// normalizeOwned sorts and merges internally produced intervals. Only true
// overlaps combine; half-open intervals that merely touch (start == last.End)
// can be distinct permuted pages and must remain separate.
func normalizeOwned(in IntervalSet) IntervalSet {
	if len(in) == 0 {
		return nil
	}
	sortSlice(in)
	out := IntervalSet{in[0]}
	for _, iv := range in[1:] {
		last := &out[len(out)-1]
		if iv.Start < last.End {
			if iv.End > last.End {
				last.End = iv.End
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

// trailingZeroBits returns the number of trailing zero bits of v (v != 0).
func trailingZeroBits(v uint64) uint {
	var k uint
	for v&1 == 0 {
		v >>= 1
		k++
	}
	return k
}
