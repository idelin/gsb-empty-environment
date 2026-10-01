package bp

// Position-level primitives. Positions are indices into the parenthesis
// string; node ids are preorder ranks 0..NumNodes()-1. The mapping between
// them is Open(v) = Select(v) and NodeAt(pos) = Rank(pos) for '(' positions.

// Rank returns the number of '(' in positions [0, i).
func (ix *Index) Rank(i int) int {
	acc := 0
	r := ix.rankAcc(i, &acc)
	ix.stats.record(OpRank, acc)
	return r
}

// Select returns the position of the k-th '(' (0-based), i.e. the opening
// parenthesis of node k.
func (ix *Index) Select(k int) int {
	acc := 0
	p := ix.selectAcc(k, &acc)
	ix.stats.record(OpSelect, acc)
	return p
}

// Excess returns opens-minus-closes before position i (0 <= i <= Len()).
func (ix *Index) Excess(i int) int {
	acc := 0
	e := ix.excessAcc(i, &acc)
	ix.stats.record(OpExcess, acc)
	return e
}

// FindClose returns the position of the ')' matching the '(' at p.
func (ix *Index) FindClose(p int) int {
	acc := 0
	c := ix.findcloseAcc(p, &acc)
	ix.stats.record(OpFindClose, acc)
	return c
}

func (ix *Index) findcloseAcc(p int, acc *int) int {
	t := ix.excessAcc(p, acc)
	r := ix.searchForward(p+1, t, acc)
	if r < 0 {
		return -1 // unreachable for a valid index
	}
	return r - 1
}

// FindOpen returns the position of the '(' matching the ')' at c.
func (ix *Index) FindOpen(c int) int {
	acc := 0
	o := ix.findopenAcc(c, &acc)
	ix.stats.record(OpFindOpen, acc)
	return o
}

func (ix *Index) findopenAcc(c int, acc *int) int {
	t := ix.excessAcc(c+1, acc)
	return ix.searchBackward(c, t, acc)
}

// Enclose returns the opening position of the parent of the node whose '('
// is at p, or -1 if p is the root.
func (ix *Index) Enclose(p int) int {
	acc := 0
	e := ix.encloseAcc(p, &acc)
	ix.stats.record(OpEnclose, acc)
	return e
}

func (ix *Index) encloseAcc(p int, acc *int) int {
	d := ix.excessAcc(p, acc)
	if d == 0 {
		return -1
	}
	return ix.searchBackward(p-1, d-1, acc)
}

// Open returns the opening-parenthesis position of node v.
func (ix *Index) Open(v int) int { return ix.Select(v) }

// NodeAt returns the node id whose '(' is at pos, and whether pos holds '('.
func (ix *Index) NodeAt(pos int) (int, bool) {
	if pos < 0 || pos >= ix.n || !ix.bits.get(pos) {
		return -1, false
	}
	return ix.Rank(pos), true
}

// --- node-level navigation (ids are preorder ranks) ---

// Depth returns the depth of node v; the root has depth 0.
func (ix *Index) Depth(v int) int {
	acc := 0
	o := ix.selectAcc(v, &acc)
	d := ix.excessAcc(o, &acc)
	ix.stats.record(OpDepth, acc)
	return d
}

// Parent returns the parent of v, or false if v is the root.
func (ix *Index) Parent(v int) (int, bool) {
	acc := 0
	o := ix.selectAcc(v, &acc)
	p := ix.encloseAcc(o, &acc)
	if p < 0 {
		ix.stats.record(OpParent, acc)
		return -1, false
	}
	id := ix.rankAcc(p, &acc)
	ix.stats.record(OpParent, acc)
	return id, true
}

// FirstChild returns the first child of v, or false if v is a leaf. In
// preorder the first child of v is always v+1.
func (ix *Index) FirstChild(v int) (int, bool) {
	acc := 0
	o := ix.selectAcc(v, &acc)
	acc++
	ok := o+1 < ix.n && ix.bits.get(o+1)
	ix.stats.record(OpFirstChild, acc)
	if !ok {
		return -1, false
	}
	return v + 1, true
}

// NextSibling returns the next sibling of v, or false if there is none.
func (ix *Index) NextSibling(v int) (int, bool) {
	acc := 0
	o := ix.selectAcc(v, &acc)
	c := ix.findcloseAcc(o, &acc)
	if c+1 < ix.n && ix.bits.get(c+1) {
		id := ix.rankAcc(c+1, &acc)
		ix.stats.record(OpNextSibling, acc)
		return id, true
	}
	ix.stats.record(OpNextSibling, acc)
	return -1, false
}

// PrevSibling returns the previous sibling of v, or false if there is none.
func (ix *Index) PrevSibling(v int) (int, bool) {
	acc := 0
	o := ix.selectAcc(v, &acc)
	if o > 0 && !ix.bits.get(o-1) {
		po := ix.findopenAcc(o-1, &acc)
		id := ix.rankAcc(po, &acc)
		ix.stats.record(OpPrevSibling, acc)
		return id, true
	}
	ix.stats.record(OpPrevSibling, acc)
	return -1, false
}

// ChildRank returns the rank of v among its parent's children (0 = first
// child, 0 also for the root). Cost is proportional to the rank.
func (ix *Index) ChildRank(v int) int {
	acc := 0
	o := ix.selectAcc(v, &acc)
	rank := 0
	for o > 0 && !ix.bits.get(o-1) {
		o = ix.findopenAcc(o-1, &acc)
		rank++
	}
	ix.stats.record(OpChildRank, acc)
	return rank
}

// Children returns the ids of v's children in order.
func (ix *Index) Children(v int) []int {
	var out []int
	c, ok := ix.FirstChild(v)
	for ok {
		out = append(out, c)
		c, ok = ix.NextSibling(c)
	}
	return out
}

// SubtreeSize returns the number of nodes in v's subtree, v included.
func (ix *Index) SubtreeSize(v int) int {
	acc := 0
	s := ix.subtreeSizeAcc(v, &acc)
	ix.stats.record(OpSubtreeSize, acc)
	return s
}

func (ix *Index) subtreeSizeAcc(v int, acc *int) int {
	o := ix.selectAcc(v, acc)
	c := ix.findcloseAcc(o, acc)
	return (c - o + 1) / 2
}

// SubtreeRange returns the preorder id interval [lo, hi) of v's subtree.
func (ix *Index) SubtreeRange(v int) (lo, hi int) {
	size := ix.SubtreeSize(v)
	return v, v + size
}

// SubtreeParens returns the parenthesis interval [open, close] of v's
// subtree; the subtree spans exactly positions open..close.
func (ix *Index) SubtreeParens(v int) (open, close int) {
	acc := 0
	o := ix.selectAcc(v, &acc)
	c := ix.findcloseAcc(o, &acc)
	ix.stats.record(OpSubtreeSize, acc)
	return o, c
}

// LCA returns the lowest common ancestor of nodes u and v.
//
// With ou < ov the opening positions, let m be the minimum excess over
// (ou, ov]; the answer is the node whose '(' is at the rightmost position
// q <= ou with Excess(q) == m-1. Both the range minimum and the backward
// search use the summary hierarchy.
func (ix *Index) LCA(u, v int) int {
	acc := 0
	if u == v {
		ix.stats.record(OpLCA, acc)
		return u
	}
	ou := ix.selectAcc(u, &acc)
	ov := ix.selectAcc(v, &acc)
	if ou > ov {
		ou, ov = ov, ou
	}
	m := ix.minExcessRange(ou+1, ov+1, &acc)
	q := ix.searchBackward(ou, m-1, &acc)
	id := ix.rankAcc(q, &acc)
	ix.stats.record(OpLCA, acc)
	return id
}
