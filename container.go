package idset

import (
	"math/bits"
	"sort"
)

// ctype is the low-16-bit container representation. The numeric values are
// also the on-the-wire type tags.
type ctype uint8

const (
	cArray  ctype = 0 // sorted []uint16
	cBitmap ctype = 1 // fixed 2^16-bit bitmap
	cRun    ctype = 2 // disjoint, non-adjacent inclusive intervals
)

const (
	bitmapBytes  = 8192 // 2^16 bits
	bitmapWords  = bitmapBytes / 8
	arrayMaxCard = 4096 // 2*4096 == bitmapBytes; ties resolve to array
)

// ival is a closed interval [lo, hi].
type ival struct{ lo, hi uint16 }

// container is immutable: every mutation returns a fresh container, so
// snapshots may share containers safely.
type container struct {
	typ  ctype
	card int
	arr  []uint16             // cArray: sorted, unique
	bm   *[bitmapWords]uint64 // cBitmap
	runs []ival               // cRun: sorted, disjoint, gap >= 2
}

func (c *container) serializedSize() int {
	switch c.typ {
	case cArray:
		return 2 * c.card
	case cBitmap:
		return bitmapBytes
	default:
		return 4 * len(c.runs)
	}
}

// pickType chooses the canonical container type: the minimum serialized
// size, ties broken by the fixed priority array > run > bitmap.
func pickType(arrSize, runSize, bmSize int) ctype {
	best, bestT := arrSize, cArray
	if runSize < best {
		best, bestT = runSize, cRun
	}
	if bmSize < best {
		bestT = cBitmap
	}
	return bestT
}

func (c *container) numRuns() int {
	switch c.typ {
	case cRun:
		return len(c.runs)
	case cArray:
		n := 0
		for i, v := range c.arr {
			if i == 0 || uint32(v) != uint32(c.arr[i-1])+1 {
				n++
			}
		}
		return n
	default:
		n := 0
		prevLast := false
		for _, w := range c.bm {
			if w == 0 {
				prevLast = false
				continue
			}
			t := bits.OnesCount64(w &^ (w << 1))
			if prevLast && w&1 != 0 {
				t--
			}
			n += t
			prevLast = w>>63 != 0
		}
		return n
	}
}

// canonical returns the canonical form of c (itself if already canonical).
func (c *container) canonical() *container {
	t := pickType(2*c.card, 4*c.numRuns(), bitmapBytes)
	switch t {
	case c.typ:
		return c
	case cArray:
		return c.toArray()
	case cRun:
		return c.toRuns()
	default:
		return c.toBitmap()
	}
}

func (c *container) toArray() *container {
	if c.typ == cArray {
		return c
	}
	arr := make([]uint16, 0, c.card)
	it := c.iter()
	for v, ok := it.next(); ok; v, ok = it.next() {
		arr = append(arr, v)
	}
	return &container{typ: cArray, card: c.card, arr: arr}
}

func (c *container) toBitmap() *container {
	if c.typ == cBitmap {
		return c
	}
	bm := new([bitmapWords]uint64)
	it := c.iter()
	for v, ok := it.next(); ok; v, ok = it.next() {
		bm[v>>6] |= 1 << (v & 63)
	}
	return &container{typ: cBitmap, card: c.card, bm: bm}
}

func (c *container) toRuns() *container {
	if c.typ == cRun {
		return c
	}
	var runs []ival
	it := c.iter()
	for v, ok := it.next(); ok; v, ok = it.next() {
		if n := len(runs); n > 0 && uint32(v) == uint32(runs[n-1].hi)+1 {
			runs[n-1].hi = v
		} else {
			runs = append(runs, ival{v, v})
		}
	}
	return &container{typ: cRun, card: c.card, runs: runs}
}

func (c *container) contains(v uint16) bool {
	switch c.typ {
	case cArray:
		i := sort.Search(len(c.arr), func(i int) bool { return c.arr[i] >= v })
		return i < len(c.arr) && c.arr[i] == v
	case cBitmap:
		return c.bm[v>>6]>>(v&63)&1 != 0
	default:
		i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].hi >= v })
		return i < len(c.runs) && c.runs[i].lo <= v
	}
}

// rank returns the number of elements <= v.
func (c *container) rank(v uint16) int {
	switch c.typ {
	case cArray:
		return sort.Search(len(c.arr), func(i int) bool { return c.arr[i] > v })
	case cBitmap:
		w := int(v >> 6)
		n := 0
		for i := 0; i < w; i++ {
			n += bits.OnesCount64(c.bm[i])
		}
		b := v & 63
		mask := uint64(1)<<(b+1) - 1
		return n + bits.OnesCount64(c.bm[w]&mask)
	default:
		n := 0
		for _, r := range c.runs {
			if v < r.lo {
				break
			}
			if v >= r.hi {
				n += int(r.hi) - int(r.lo) + 1
			} else {
				n += int(v) - int(r.lo) + 1
			}
		}
		return n
	}
}

// selectK returns the k-th (0-based) element; caller guarantees k < card.
func (c *container) selectK(k int) uint16 {
	switch c.typ {
	case cArray:
		return c.arr[k]
	case cBitmap:
		for w := 0; w < bitmapWords; w++ {
			pc := bits.OnesCount64(c.bm[w])
			if k >= pc {
				k -= pc
				continue
			}
			word := c.bm[w]
			for ; k > 0; k-- {
				word &= word - 1
			}
			return uint16(w*64 + bits.TrailingZeros64(word))
		}
		panic("unreachable")
	default:
		for _, r := range c.runs {
			l := int(r.hi) - int(r.lo) + 1
			if k < l {
				return r.lo + uint16(k)
			}
			k -= l
		}
		panic("unreachable")
	}
}

// add returns the container with v inserted and the cardinality delta.
func (c *container) add(v uint16) (*container, int) {
	switch c.typ {
	case cArray:
		i := sort.Search(len(c.arr), func(i int) bool { return c.arr[i] >= v })
		if i < len(c.arr) && c.arr[i] == v {
			return c, 0
		}
		na := make([]uint16, 0, len(c.arr)+1)
		na = append(na, c.arr[:i]...)
		na = append(na, v)
		na = append(na, c.arr[i:]...)
		return &container{typ: cArray, card: c.card + 1, arr: na}, 1
	case cBitmap:
		w, b := v>>6, v&63
		if c.bm[w]>>b&1 != 0 {
			return c, 0
		}
		nb := *c.bm
		nb[w] |= 1 << b
		return &container{typ: cBitmap, card: c.card + 1, bm: &nb}, 1
	default:
		runs := c.runs
		i := sort.Search(len(runs), func(i int) bool { return runs[i].hi >= v })
		if i < len(runs) && runs[i].lo <= v {
			return c, 0
		}
		mergeLeft := i > 0 && uint32(runs[i-1].hi)+1 == uint32(v)
		mergeRight := i < len(runs) && uint32(v)+1 == uint32(runs[i].lo)
		switch {
		case mergeLeft && mergeRight:
			nr := make([]ival, 0, len(runs)-1)
			nr = append(nr, runs[:i-1]...)
			nr = append(nr, ival{runs[i-1].lo, runs[i].hi})
			nr = append(nr, runs[i+1:]...)
			return &container{typ: cRun, card: c.card + 1, runs: nr}, 1
		case mergeLeft:
			nr := append([]ival(nil), runs...)
			nr[i-1].hi = v
			return &container{typ: cRun, card: c.card + 1, runs: nr}, 1
		case mergeRight:
			nr := append([]ival(nil), runs...)
			nr[i].lo = v
			return &container{typ: cRun, card: c.card + 1, runs: nr}, 1
		default:
			nr := make([]ival, 0, len(runs)+1)
			nr = append(nr, runs[:i]...)
			nr = append(nr, ival{v, v})
			nr = append(nr, runs[i:]...)
			return &container{typ: cRun, card: c.card + 1, runs: nr}, 1
		}
	}
}

// remove returns the container with v deleted and the cardinality delta.
// A nil container means the block became empty.
func (c *container) remove(v uint16) (*container, int) {
	switch c.typ {
	case cArray:
		i := sort.Search(len(c.arr), func(i int) bool { return c.arr[i] >= v })
		if i == len(c.arr) || c.arr[i] != v {
			return c, 0
		}
		na := make([]uint16, 0, len(c.arr)-1)
		na = append(na, c.arr[:i]...)
		na = append(na, c.arr[i+1:]...)
		return &container{typ: cArray, card: c.card - 1, arr: na}, -1
	case cBitmap:
		w, b := v>>6, v&63
		if c.bm[w]>>b&1 == 0 {
			return c, 0
		}
		nb := *c.bm
		nb[w] &^= 1 << b
		return &container{typ: cBitmap, card: c.card - 1, bm: &nb}, -1
	default:
		runs := c.runs
		i := sort.Search(len(runs), func(i int) bool { return runs[i].hi >= v })
		if i == len(runs) || runs[i].lo > v {
			return c, 0
		}
		r := runs[i]
		switch {
		case r.lo == v && r.hi == v:
			nr := make([]ival, 0, len(runs)-1)
			nr = append(nr, runs[:i]...)
			nr = append(nr, runs[i+1:]...)
			return &container{typ: cRun, card: c.card - 1, runs: nr}, -1
		case r.lo == v:
			nr := append([]ival(nil), runs...)
			nr[i].lo = v + 1
			return &container{typ: cRun, card: c.card - 1, runs: nr}, -1
		case r.hi == v:
			nr := append([]ival(nil), runs...)
			nr[i].hi = v - 1
			return &container{typ: cRun, card: c.card - 1, runs: nr}, -1
		default:
			nr := make([]ival, 0, len(runs)+1)
			nr = append(nr, runs[:i]...)
			nr = append(nr, ival{r.lo, v - 1}, ival{v + 1, r.hi})
			nr = append(nr, runs[i+1:]...)
			return &container{typ: cRun, card: c.card - 1, runs: nr}, -1
		}
	}
}

// citer iterates a container's values in increasing order.
type citer struct {
	c    *container
	idx  int
	word int
	bits uint64
	base int
	on   bool
	cur  uint32
}

func (c *container) iter() *citer { return &citer{c: c} }

func (it *citer) next() (uint16, bool) {
	switch it.c.typ {
	case cArray:
		if it.idx >= len(it.c.arr) {
			return 0, false
		}
		v := it.c.arr[it.idx]
		it.idx++
		return v, true
	case cBitmap:
		for it.bits == 0 {
			if it.word >= bitmapWords {
				return 0, false
			}
			it.bits = it.c.bm[it.word]
			it.base = it.word * 64
			it.word++
		}
		tz := bits.TrailingZeros64(it.bits)
		it.bits &= it.bits - 1
		return uint16(it.base + tz), true
	default:
		runs := it.c.runs
		if !it.on {
			if len(runs) == 0 {
				return 0, false
			}
			it.on = true
			it.cur = uint32(runs[0].lo)
			return uint16(it.cur), true
		}
		if it.cur < uint32(runs[it.idx].hi) {
			it.cur++
			return uint16(it.cur), true
		}
		it.idx++
		if it.idx >= len(runs) {
			return 0, false
		}
		it.cur = uint32(runs[it.idx].lo)
		return uint16(it.cur), true
	}
}

// builder accumulates values in increasing order, switching from array to
// bitmap once the array would exceed arrayMaxCard.
type builder struct {
	arr []uint16
	bm  *[bitmapWords]uint64
	n   int
}

func (b *builder) add(v uint16) {
	b.n++
	if b.bm != nil {
		b.bm[v>>6] |= 1 << (v & 63)
		return
	}
	if len(b.arr) < arrayMaxCard {
		b.arr = append(b.arr, v)
		return
	}
	bm := new([bitmapWords]uint64)
	for _, x := range b.arr {
		bm[x>>6] |= 1 << (x & 63)
	}
	b.arr = nil
	b.bm = bm
	b.bm[v>>6] |= 1 << (v & 63)
}

// finish returns nil for an empty result.
func (b *builder) finish() *container {
	if b.n == 0 {
		return nil
	}
	if b.bm != nil {
		return &container{typ: cBitmap, card: b.n, bm: b.bm}
	}
	return &container{typ: cArray, card: b.n, arr: b.arr}
}

type setOp int

const (
	opAnd setOp = iota
	opOr
	opXor
)

// combine merges two containers value-by-value; it never materializes more
// than one 2^16-bit block.
func combine(a, b *container, op setOp) *container {
	ia, ib := a.iter(), b.iter()
	var bd builder
	va, oka := ia.next()
	vb, okb := ib.next()
	for oka && okb {
		switch {
		case va < vb:
			if op != opAnd {
				bd.add(va)
			}
			va, oka = ia.next()
		case vb < va:
			if op != opAnd {
				bd.add(vb)
			}
			vb, okb = ib.next()
		default:
			if op != opXor {
				bd.add(va)
			}
			va, oka = ia.next()
			vb, okb = ib.next()
		}
	}
	if op != opAnd {
		for oka {
			bd.add(va)
			va, oka = ia.next()
		}
		for okb {
			bd.add(vb)
			vb, okb = ib.next()
		}
	}
	return bd.finish()
}
