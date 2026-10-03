package bitmap

import "math/bits"

const bitmapWords = 1024

// bitmapContainer is a fixed 65536-bit (8 KiB) bitmap.
type bitmapContainer struct {
	bits [bitmapWords]uint64
	card int
}

func newBitmapContainer() *bitmapContainer { return &bitmapContainer{} }

func (bc *bitmapContainer) typeName() string { return "bitmap" }

func (bc *bitmapContainer) cardinality() int { return bc.card }

func (bc *bitmapContainer) contains(x uint16) bool {
	return bc.bits[x>>6]&(uint64(1)<<(x&63)) != 0
}

func (bc *bitmapContainer) set(x uint16) {
	w := x >> 6
	m := uint64(1) << (x & 63)
	if bc.bits[w]&m == 0 {
		bc.bits[w] |= m
		bc.card++
	}
}

func (bc *bitmapContainer) unset(x uint16) {
	w := x >> 6
	m := uint64(1) << (x & 63)
	if bc.bits[w]&m != 0 {
		bc.bits[w] &^= m
		bc.card--
	}
}

func (bc *bitmapContainer) add(x uint16) (container, bool) {
	w := x >> 6
	m := uint64(1) << (x & 63)
	if bc.bits[w]&m != 0 {
		return bc, false
	}
	bc.bits[w] |= m
	bc.card++
	return bc, true
}

// rangeMask returns a word mask of bits lo..hi, valid when lo>>6 == hi>>6.
func rangeMask(lo, hi uint16) uint64 {
	return (^uint64(0) << (lo & 63)) & (^uint64(0) >> (63 - (hi & 63)))
}

func (bc *bitmapContainer) addRange(lo, hi uint16) (container, bool) {
	before := bc.card
	first, last := lo>>6, hi>>6
	if first == last {
		bc.bits[first] |= rangeMask(lo, hi)
	} else {
		bc.bits[first] |= ^uint64(0) << (lo & 63)
		for w := first + 1; w < last; w++ {
			bc.bits[w] = ^uint64(0)
		}
		bc.bits[last] |= ^uint64(0) >> (63 - (hi & 63))
	}
	bc.recount()
	return bc, bc.card != before
}

func (bc *bitmapContainer) flipRange(lo, hi uint16) {
	first, last := lo>>6, hi>>6
	if first == last {
		bc.bits[first] ^= rangeMask(lo, hi)
	} else {
		bc.bits[first] ^= ^uint64(0) << (lo & 63)
		for w := first + 1; w < last; w++ {
			bc.bits[w] = ^bc.bits[w]
		}
		bc.bits[last] ^= ^uint64(0) >> (63 - (hi & 63))
	}
	bc.recount()
}

func (bc *bitmapContainer) recount() {
	c := 0
	for i := 0; i < bitmapWords; i++ {
		c += bits.OnesCount64(bc.bits[i])
	}
	bc.card = c
}

// numRuns counts runs of consecutive set bits, word at a time.
func (bc *bitmapContainer) numRuns() int {
	runs := 0
	var prev uint64
	for i := 0; i < bitmapWords; i++ {
		w := bc.bits[i]
		if w == 0 {
			prev = 0
			continue
		}
		runs += bits.OnesCount64(w &^ (w << 1))
		if prev&(uint64(1)<<63) != 0 && w&1 != 0 {
			runs--
		}
		prev = w
	}
	return runs
}

func (bc *bitmapContainer) toBitmap() *bitmapContainer { return bc }

func (bc *bitmapContainer) foreach(fn func(x uint16) bool) {
	for w := 0; w < bitmapWords; w++ {
		word := bc.bits[w]
		for word != 0 {
			t := bits.TrailingZeros64(word)
			if !fn(uint16(w<<6 + t)) {
				return
			}
			word &= word - 1
		}
	}
}

type bitmapIterator struct {
	bc   *bitmapContainer
	w    int
	word uint64
}

func (bc *bitmapContainer) iter() containerIterator {
	return &bitmapIterator{bc: bc, word: bc.bits[0]}
}

func (it *bitmapIterator) next() (uint16, bool) {
	for {
		if it.word != 0 {
			t := bits.TrailingZeros64(it.word)
			it.word &= it.word - 1
			return uint16(it.w<<6 + t), true
		}
		it.w++
		if it.w >= bitmapWords {
			return 0, false
		}
		it.word = it.bc.bits[it.w]
	}
}

func (bc *bitmapContainer) equals(o container) bool {
	oc, ok := o.(*bitmapContainer)
	return ok && oc.card == bc.card && oc.bits == bc.bits
}

func (bc *bitmapContainer) clone() container {
	c := *bc
	return &c
}

// bitmapToRun converts a bitmap container to a run container scanning words.
func bitmapToRun(bc *bitmapContainer) *runContainer {
	out := &runContainer{}
	for w := 0; w < bitmapWords; w++ {
		word := bc.bits[w]
		for word != 0 {
			start := bits.TrailingZeros64(word)
			runLen := bits.TrailingZeros64(^(word >> start))
			lo := w<<6 + start
			hi := lo + runLen - 1
			if n := len(out.runs); n > 0 && uint32(out.runs[n-1].last)+1 == uint32(lo) {
				out.runs[n-1].last = uint16(hi)
			} else {
				out.runs = append(out.runs, interval16{uint16(lo), uint16(hi)})
			}
			if runLen == 64 {
				word = 0
			} else {
				word &= ^(((uint64(1) << runLen) - 1) << start)
			}
		}
	}
	return out
}
