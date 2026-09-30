package roaring

import (
	"math/bits"
	"sort"
)

const (
	bitmapWords = 1024            // 65536 bits
	bitmapBytes = bitmapWords * 8 // 8192
)

// ctype is the physical encoding of one high-16-bit bucket.
type ctype uint8

const (
	cArray  ctype = iota // sorted []uint16, 2 bytes/value
	cBitmap              // fixed []uint64 bitmap, 8192 bytes
	cRun                 // inclusive [start,last] intervals, 4 bytes/run
)

func (t ctype) String() string {
	switch t {
	case cArray:
		return "array"
	case cBitmap:
		return "bitmap"
	case cRun:
		return "run"
	}
	return "unknown"
}

type interval struct{ start, last uint16 } // inclusive

// container stores the low 16 bits of every value in one bucket.
// Exactly one of arr/bmp/runs is live, selected by typ.
type container struct {
	typ  ctype
	arr  []uint16
	bmp  []uint64
	runs []interval
	card int
}

// byteSize reports the real encoded payload size of the live representation.
func (c *container) byteSize() int {
	switch c.typ {
	case cArray:
		return 2 * len(c.arr)
	case cBitmap:
		return bitmapBytes
	case cRun:
		return 4 * len(c.runs)
	}
	return 0
}

func searchUint16(a []uint16, v uint16) int {
	return sort.Search(len(a), func(i int) bool { return a[i] >= v })
}

func (c *container) contains(v uint16) bool {
	switch c.typ {
	case cArray:
		i := searchUint16(c.arr, v)
		return i < len(c.arr) && c.arr[i] == v
	case cBitmap:
		return c.bmp[v>>6]&(uint64(1)<<(v&63)) != 0
	case cRun:
		i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].last >= v })
		return i < len(c.runs) && c.runs[i].start <= v
	}
	return false
}

// add inserts v, returns true if the container changed.
func (c *container) add(v uint16) bool {
	switch c.typ {
	case cArray:
		i := searchUint16(c.arr, v)
		if i < len(c.arr) && c.arr[i] == v {
			return false
		}
		c.arr = append(c.arr, 0)
		copy(c.arr[i+1:], c.arr[i:])
		c.arr[i] = v
		c.card++
		return true
	case cBitmap:
		w, m := v>>6, uint64(1)<<(v&63)
		if c.bmp[w]&m != 0 {
			return false
		}
		c.bmp[w] |= m
		c.card++
		return true
	case cRun:
		return c.runAdd(v)
	}
	return false
}

func (c *container) runAdd(v uint16) bool {
	i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].last >= v })
	if i < len(c.runs) && c.runs[i].start <= v {
		return false // already covered
	}
	extendPrev := i > 0 && uint32(c.runs[i-1].last)+1 == uint32(v)
	extendNext := i < len(c.runs) && uint32(v)+1 == uint32(c.runs[i].start)
	switch {
	case extendPrev && extendNext:
		c.runs[i-1].last = c.runs[i].last
		c.runs = append(c.runs[:i], c.runs[i+1:]...)
	case extendPrev:
		c.runs[i-1].last = v
	case extendNext:
		c.runs[i].start = v
	default:
		c.runs = append(c.runs, interval{})
		copy(c.runs[i+1:], c.runs[i:])
		c.runs[i] = interval{v, v}
	}
	c.card++
	return true
}

// remove deletes v, returns true if the container changed.
func (c *container) remove(v uint16) bool {
	switch c.typ {
	case cArray:
		i := searchUint16(c.arr, v)
		if i >= len(c.arr) || c.arr[i] != v {
			return false
		}
		c.arr = append(c.arr[:i], c.arr[i+1:]...)
		c.card--
		return true
	case cBitmap:
		w, m := v>>6, uint64(1)<<(v&63)
		if c.bmp[w]&m == 0 {
			return false
		}
		c.bmp[w] &^= m
		c.card--
		return true
	case cRun:
		return c.runRemove(v)
	}
	return false
}

func (c *container) runRemove(v uint16) bool {
	i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].last >= v })
	if i >= len(c.runs) || c.runs[i].start > v {
		return false
	}
	r := c.runs[i]
	switch {
	case r.start == v && r.last == v:
		c.runs = append(c.runs[:i], c.runs[i+1:]...)
	case r.start == v:
		c.runs[i].start = v + 1
	case r.last == v:
		c.runs[i].last = v - 1
	default: // split into two runs
		c.runs = append(c.runs, interval{})
		copy(c.runs[i+2:], c.runs[i+1:])
		c.runs[i].last = v - 1
		c.runs[i+1] = interval{v + 1, r.last}
	}
	c.card--
	return true
}

// numRuns counts maximal runs of consecutive values in the live encoding.
func (c *container) numRuns() int {
	switch c.typ {
	case cRun:
		return len(c.runs)
	case cArray:
		if len(c.arr) == 0 {
			return 0
		}
		n := 1
		for i := 1; i < len(c.arr); i++ {
			if uint32(c.arr[i]) != uint32(c.arr[i-1])+1 {
				n++
			}
		}
		return n
	case cBitmap:
		n := 0
		var carry uint64 // previous word's top bit, shifted into bit 0
		for _, w := range c.bmp {
			n += bits.OnesCount64(w &^ ((w << 1) | carry))
			carry = w >> 63
		}
		return n
	}
	return 0
}

// iterator streams values in ascending order regardless of encoding.
type iterator struct {
	c     *container
	ai    int
	wi    int
	curw  int
	word  uint64
	ri    int
	cur   uint32
	rinit bool
}

func (c *container) iter() *iterator { return &iterator{c: c} }

func (it *iterator) next() (uint16, bool) {
	c := it.c
	switch c.typ {
	case cArray:
		if it.ai >= len(c.arr) {
			return 0, false
		}
		v := c.arr[it.ai]
		it.ai++
		return v, true
	case cRun:
		for {
			if it.ri >= len(c.runs) {
				return 0, false
			}
			r := c.runs[it.ri]
			if !it.rinit {
				it.cur = uint32(r.start)
				it.rinit = true
			}
			if it.cur <= uint32(r.last) {
				v := uint16(it.cur)
				it.cur++
				return v, true
			}
			it.ri++
			it.rinit = false
		}
	case cBitmap:
		for {
			if it.word == 0 {
				if it.wi >= bitmapWords {
					return 0, false
				}
				it.word = c.bmp[it.wi]
				it.curw = it.wi
				it.wi++
				continue
			}
			b := bits.TrailingZeros64(it.word)
			it.word &= it.word - 1
			return uint16(it.curw*64 + b), true
		}
	}
	return 0, false
}

func (c *container) foreach(f func(uint16)) {
	it := c.iter()
	for v, ok := it.next(); ok; v, ok = it.next() {
		f(v)
	}
}

// convertTo re-encodes c into representation t, preserving the value set.
func (c *container) convertTo(t ctype) *container {
	if c.typ == t {
		return c
	}
	nc := &container{typ: t, card: c.card}
	switch t {
	case cArray:
		nc.arr = make([]uint16, 0, c.card)
		c.foreach(func(v uint16) { nc.arr = append(nc.arr, v) })
	case cBitmap:
		nc.bmp = make([]uint64, bitmapWords)
		c.foreach(func(v uint16) { nc.bmp[v>>6] |= uint64(1) << (v & 63) })
	case cRun:
		it := c.iter()
		v, ok := it.next()
		for ok {
			last := v
			start := v
			for {
				v, ok = it.next()
				if !ok || uint32(v) != uint32(last)+1 {
					break
				}
				last = v
			}
			nc.runs = append(nc.runs, interval{start, last})
		}
	}
	return nc
}

func (c *container) clone() *container {
	nc := &container{typ: c.typ, card: c.card}
	nc.arr = append([]uint16(nil), c.arr...)
	nc.bmp = append([]uint64(nil), c.bmp...)
	nc.runs = append([]interval(nil), c.runs...)
	return nc
}

func bitmapCardinality(bmp []uint64) int {
	n := 0
	for _, w := range bmp {
		n += bits.OnesCount64(w)
	}
	return n
}

// normalize picks the cheapest legal encoding for c.
//
// Legality (hard bounds): an array may hold at most cfg.ArrayMax values, a
// bitmap is only kept while cardinality >= cfg.BitmapMin. Because
// BitmapMin < ArrayMax there is a hysteresis band in which both encodings
// are legal and the current one is kept, so add/remove of a single element
// near the threshold cannot oscillate.
//
// Among legal encodings a switch happens only when it saves at least
// cfg.HysteresisBytes bytes (or the current encoding became illegal).
func normalize(c *container, cfg Config) *container {
	runs := c.numRuns()
	arrayBytes := 2 * c.card
	runBytes := 4 * runs

	arrayOK := c.card <= cfg.ArrayMax
	bitmapOK := c.card >= cfg.BitmapMin

	forced := (c.typ == cArray && !arrayOK) || (c.typ == cBitmap && !bitmapOK)

	best, bestSize := c.typ, c.byteSize()
	if forced {
		bestSize = int(^uint(0) >> 1) // current encoding is illegal, must move
	}
	consider := func(t ctype, size int, ok bool) {
		if ok && size < bestSize {
			best, bestSize = t, size
		}
	}
	consider(cArray, arrayBytes, arrayOK)
	consider(cBitmap, bitmapBytes, bitmapOK)
	consider(cRun, runBytes, true)

	if best == c.typ {
		return c
	}
	if !forced && bestSize+cfg.HysteresisBytes >= c.byteSize() {
		return c // saving too small: keep current encoding (anti-thrash)
	}
	return c.convertTo(best)
}
