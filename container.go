package crset

import (
	"math/bits"
	"sort"
)

// repKind is the physical encoding of one container (one high-16-bit bucket).
type repKind uint8

const (
	repSparse repKind = iota // sorted []uint16
	repBitmap                // 65536-bit dense bitmap
	repRun                   // run-length encoded intervals
)

func (r repKind) String() string {
	switch r {
	case repSparse:
		return "sparse"
	case repBitmap:
		return "bitmap"
	case repRun:
		return "run"
	}
	return "unknown"
}

// Physical layout constants (the byte accounting in Stats uses exactly these).
const (
	bitmapWords = 8192            // 8192 * 64 bits = 65536 bits
	bitmapBytes = bitmapWords * 8 // 64 KiB

	containerHeaderBytes = 3 // 1 byte type tag + 2 bytes cardinality
	bucketKeyBytes       = 2 // uint16 high key in the sorted top-level directory
	runSizeBytes         = 4 // uint16 start + uint16 length

	// minRunCard is the minimum cardinality at which a container may switch
	// to RLE. Together with RunSavingsFactor >= 2 this guarantees the
	// hysteresis band is always wider than any single-mutation delta, so
	// add/remove hovering near a threshold can never flip the
	// representation back and forth.
	minRunCard = 16
)

// run is an inclusive interval [start, start+length] of uint16 values.
type run struct {
	start  uint16
	length uint16
}

func (r run) last() uint16 { return r.start + r.length }

// container stores the low 16 bits of one bucket in one of three encodings.
// Exactly one of sparse/bitmap/runs is non-nil, selected by rep.
type container struct {
	rep    repKind
	card   int
	sparse []uint16 // repSparse: sorted ascending, no duplicates
	bitmap []uint64 // repBitmap: exactly bitmapWords words
	runs   []run    // repRun: sorted, non-overlapping, non-adjacent
}

// payloadBytes is the exact on-disk size of the container payload.
func (c *container) payloadBytes() int {
	switch c.rep {
	case repSparse:
		return 2 * c.card
	case repBitmap:
		return bitmapBytes
	case repRun:
		return runSizeBytes * len(c.runs)
	}
	return 0
}

func (c *container) contains(v uint16) bool {
	switch c.rep {
	case repSparse:
		i := sort.Search(len(c.sparse), func(i int) bool { return c.sparse[i] >= v })
		return i < len(c.sparse) && c.sparse[i] == v
	case repBitmap:
		return c.bitmap[v>>6]&(uint64(1)<<(v&63)) != 0
	case repRun:
		i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].last() >= v })
		return i < len(c.runs) && c.runs[i].start <= v
	}
	return false
}

// add inserts v, reporting whether it was not already present.
func (c *container) add(v uint16) bool {
	switch c.rep {
	case repSparse:
		i := sort.Search(len(c.sparse), func(i int) bool { return c.sparse[i] >= v })
		if i < len(c.sparse) && c.sparse[i] == v {
			return false
		}
		c.sparse = append(c.sparse, 0)
		copy(c.sparse[i+1:], c.sparse[i:])
		c.sparse[i] = v
		c.card++
		return true
	case repBitmap:
		w := &c.bitmap[v>>6]
		mask := uint64(1) << (v & 63)
		if *w&mask != 0 {
			return false
		}
		*w |= mask
		c.card++
		return true
	case repRun:
		return c.runAdd(v)
	}
	return false
}

// remove deletes v, reporting whether it was present.
func (c *container) remove(v uint16) bool {
	switch c.rep {
	case repSparse:
		i := sort.Search(len(c.sparse), func(i int) bool { return c.sparse[i] >= v })
		if i == len(c.sparse) || c.sparse[i] != v {
			return false
		}
		c.sparse = append(c.sparse[:i], c.sparse[i+1:]...)
		c.card--
		return true
	case repBitmap:
		w := &c.bitmap[v>>6]
		mask := uint64(1) << (v & 63)
		if *w&mask == 0 {
			return false
		}
		*w &^= mask
		c.card--
		return true
	case repRun:
		return c.runRemove(v)
	}
	return false
}

func (c *container) runAdd(v uint16) bool {
	// i = index of first run with start > v; the run that could contain v is i-1.
	i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].start > v })
	if i > 0 {
		prev := &c.runs[i-1]
		last := uint32(prev.start) + uint32(prev.length)
		switch {
		case uint32(v) <= last:
			return false // already present
		case uint32(v) == last+1:
			prev.length++
			// Bridge to the next run? Then merge them.
			if i < len(c.runs) && uint32(c.runs[i].start) == last+2 {
				prev.length += c.runs[i].length + 1
				c.runs = append(c.runs[:i], c.runs[i+1:]...)
			}
			c.card++
			return true
		}
	}
	if i < len(c.runs) && uint32(c.runs[i].start) == uint32(v)+1 {
		c.runs[i].start = v
		c.runs[i].length++
		c.card++
		return true
	}
	c.runs = append(c.runs, run{})
	copy(c.runs[i+1:], c.runs[i:])
	c.runs[i] = run{start: v}
	c.card++
	return true
}

func (c *container) runRemove(v uint16) bool {
	i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].last() >= v })
	if i == len(c.runs) || c.runs[i].start > v {
		return false
	}
	r := c.runs[i]
	switch {
	case r.start == v && r.length == 0:
		c.runs = append(c.runs[:i], c.runs[i+1:]...)
	case r.start == v:
		c.runs[i].start++
		c.runs[i].length--
	case r.last() == v:
		c.runs[i].length--
	default:
		// v is strictly inside the run: split into two runs.
		left := run{start: r.start, length: v - r.start - 1}
		right := run{start: v + 1, length: r.last() - v - 1}
		c.runs[i] = left
		c.runs = append(c.runs, run{})
		copy(c.runs[i+2:], c.runs[i+1:])
		c.runs[i+1] = right
	}
	c.card--
	return true
}

// containerIter streams a container's values in ascending order.
type containerIter struct {
	c    *container
	idx  int    // sparse: element index; run: run index
	widx int    // bitmap: current word index
	word uint64 // bitmap: remaining bits of current word
	cur  uint32 // run: next value to emit in current run
	end  uint32 // run: last value of current run
}

func (c *container) iter() *containerIter {
	it := &containerIter{c: c}
	switch c.rep {
	case repBitmap:
		it.word = c.bitmap[0]
	case repRun:
		if len(c.runs) > 0 {
			it.cur = uint32(c.runs[0].start)
			it.end = uint32(c.runs[0].last())
		}
	}
	return it
}

func (it *containerIter) next() (uint16, bool) {
	switch it.c.rep {
	case repSparse:
		if it.idx < len(it.c.sparse) {
			v := it.c.sparse[it.idx]
			it.idx++
			return v, true
		}
	case repBitmap:
		for {
			if it.word != 0 {
				v := uint16(it.widx*64 + bits.TrailingZeros64(it.word))
				it.word &= it.word - 1
				return v, true
			}
			it.widx++
			for it.widx < bitmapWords && it.c.bitmap[it.widx] == 0 {
				it.widx++
			}
			if it.widx >= bitmapWords {
				return 0, false
			}
			it.word = it.c.bitmap[it.widx]
		}
	case repRun:
		if it.idx >= len(it.c.runs) {
			return 0, false
		}
		v := it.cur
		if it.cur == it.end {
			it.idx++
			if it.idx < len(it.c.runs) {
				it.cur = uint32(it.c.runs[it.idx].start)
				it.end = uint32(it.c.runs[it.idx].last())
			}
		} else {
			it.cur++
		}
		return uint16(v), true
	}
	return 0, false
}

// runCount returns how many maximal contiguous runs the container holds.
func (c *container) runCount() int {
	switch c.rep {
	case repRun:
		return len(c.runs)
	case repSparse:
		if len(c.sparse) == 0 {
			return 0
		}
		n := 1
		for i := 1; i < len(c.sparse); i++ {
			if c.sparse[i] != c.sparse[i-1]+1 {
				n++
			}
		}
		return n
	case repBitmap:
		// Count 0->1 transitions; carry is the previous word's top bit.
		n := 0
		carry := uint64(0)
		for _, w := range c.bitmap {
			n += bits.OnesCount64(w &^ ((w << 1) | carry))
			carry = w >> 63
		}
		return n
	}
	return 0
}

func (c *container) toBitmap() {
	b := make([]uint64, bitmapWords)
	it := c.iter()
	for {
		v, ok := it.next()
		if !ok {
			break
		}
		b[v>>6] |= uint64(1) << (v & 63)
	}
	c.bitmap = b
	c.sparse = nil
	c.runs = nil
	c.rep = repBitmap
}

func (c *container) toSparse() {
	s := make([]uint16, 0, c.card)
	it := c.iter()
	for {
		v, ok := it.next()
		if !ok {
			break
		}
		s = append(s, v)
	}
	c.sparse = s
	c.bitmap = nil
	c.runs = nil
	c.rep = repSparse
}

func (c *container) toRun() {
	var runs []run
	it := c.iter()
	started := false
	var cur run
	for {
		v, ok := it.next()
		if !ok {
			break
		}
		switch {
		case !started:
			cur = run{start: v}
			started = true
		case uint32(v) == uint32(cur.last())+1:
			cur.length++
		default:
			runs = append(runs, cur)
			cur = run{start: v}
		}
	}
	if started {
		runs = append(runs, cur)
	}
	c.runs = runs
	c.sparse = nil
	c.bitmap = nil
	c.rep = repRun
}

// altBytes is the byte size of the non-RLE encoding a container of this
// cardinality would use, and is the exit threshold for RLE containers.
// The boundary is BitmapToSparseCard (not SparseToBitmapCard) so that a
// container sitting in the sparse/bitmap hysteresis band compares RLE
// against the bitmap cost; using the sparse cost there would let a single
// add/remove pair across SparseToBitmapCard flip sparse->bitmap->run->sparse
// forever.
func altBytes(cfg Config, card int) int {
	if card > cfg.BitmapToSparseCard {
		return bitmapBytes
	}
	return 2 * card
}

// maybeToRun converts to RLE when RLE is at least RunSavingsFactor times
// smaller than the current encoding.
func (c *container) maybeToRun(cfg Config) bool {
	if cfg.RunSavingsFactor == 0 || c.card < minRunCard {
		return false
	}
	runs := c.runCount()
	if runs*runSizeBytes*cfg.RunSavingsFactor <= c.payloadBytes() {
		c.toRun()
		return true
	}
	return false
}

// normalize enforces the representation policy after a mutation. It returns
// true when a conversion happened. Hysteresis bands:
//
//	sparse -> bitmap when card >  SparseToBitmapCard
//	bitmap -> sparse when card <  BitmapToSparseCard   (< SparseToBitmapCard)
//	any   -> run    when runBytes*RunSavingsFactor <= currentBytes
//	run   -> best   when runBytes > altBytes(card)  (see altBytes)
//
// The enter/exit thresholds differ by a factor (or an absolute band), so a
// single add/remove can never cross both and cause flip-flopping.
func (c *container) normalize(cfg Config) bool {
	switch c.rep {
	case repSparse:
		if c.card > cfg.SparseToBitmapCard {
			c.toBitmap()
			c.maybeToRun(cfg)
			return true
		}
		return c.maybeToRun(cfg)
	case repBitmap:
		if c.card < cfg.BitmapToSparseCard {
			c.toSparse()
			c.maybeToRun(cfg)
			return true
		}
		// Amortize the O(8192) run scan over 64 mutations.
		if c.card%64 == 0 {
			return c.maybeToRun(cfg)
		}
	case repRun:
		if cfg.RunSavingsFactor != 0 && len(c.runs)*runSizeBytes > altBytes(cfg, c.card) {
			if c.card > cfg.SparseToBitmapCard {
				c.toBitmap()
			} else {
				c.toSparse()
			}
			return true
		}
	}
	return false
}

func (c *container) clone() *container {
	out := &container{rep: c.rep, card: c.card}
	switch c.rep {
	case repSparse:
		out.sparse = append([]uint16(nil), c.sparse...)
	case repBitmap:
		out.bitmap = append([]uint64(nil), c.bitmap...)
	case repRun:
		out.runs = append([]run(nil), c.runs...)
	}
	return out
}
