package roaring

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxCardinality is the threshold above which an array container
	// is promoted to a bitmap container.
	arrayMaxCardinality = 4096
	// bitmapWords is the number of uint64 words in a bitmap container
	// (65536 bits = 8192 bytes).
	bitmapWords = 1024
	// containerCapacity is the maximum number of values in one container.
	containerCapacity = 1 << 16

	containerTypeArray  byte = 1
	containerTypeBitmap byte = 2
	containerTypeRun    byte = 3
)

// container is the internal representation of 16 low bits of a key space.
// Implementations: arrayContainer, bitmapContainer, runContainer.
type container interface {
	typ() byte
	cardinality() int
	contains(v uint16) bool
	clone() container
	toBitmap() *bitmapContainer
	toRun() *runContainer
	toArray() *arrayContainer
	serializedSize() int
}

// ---------------------------------------------------------------------------
// arrayContainer: sorted slice of values, compact for sparse data.
// ---------------------------------------------------------------------------

type arrayContainer struct{ vals []uint16 }

func (a *arrayContainer) typ() byte        { return containerTypeArray }
func (a *arrayContainer) cardinality() int { return len(a.vals) }

func (a *arrayContainer) contains(v uint16) bool {
	i := searchUint16s(a.vals, v)
	return i < len(a.vals) && a.vals[i] == v
}

func (a *arrayContainer) clone() container {
	vals := make([]uint16, len(a.vals))
	copy(vals, a.vals)
	return &arrayContainer{vals: vals}
}

func (a *arrayContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{words: make([]uint64, bitmapWords), card: len(a.vals)}
	for _, v := range a.vals {
		bc.words[v>>6] |= 1 << (v & 63)
	}
	return bc
}

func (a *arrayContainer) toRun() *runContainer {
	rc := &runContainer{}
	if len(a.vals) == 0 {
		return rc
	}
	start, prev := a.vals[0], a.vals[0]
	for _, v := range a.vals[1:] {
		if v != prev+1 {
			rc.iv = append(rc.iv, runInterval{start: start, length: prev - start})
			start = v
		}
		prev = v
	}
	rc.iv = append(rc.iv, runInterval{start: start, length: prev - start})
	return rc
}

func (a *arrayContainer) toArray() *arrayContainer { return a }

func (a *arrayContainer) serializedSize() int { return 2 * len(a.vals) }

// ---------------------------------------------------------------------------
// bitmapContainer: fixed 8KiB bitset, best for dense data.
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	words []uint64
	card  int
}

func (b *bitmapContainer) typ() byte        { return containerTypeBitmap }
func (b *bitmapContainer) cardinality() int { return b.card }

func (b *bitmapContainer) contains(v uint16) bool {
	return b.words[v>>6]&(1<<(v&63)) != 0
}

func (b *bitmapContainer) clone() container {
	words := make([]uint64, len(b.words))
	copy(words, b.words)
	return &bitmapContainer{words: words, card: b.card}
}

func (b *bitmapContainer) toBitmap() *bitmapContainer { return b }

func (b *bitmapContainer) toArray() *arrayContainer {
	vals := make([]uint16, 0, b.card)
	for i := 0; i < bitmapWords; i++ {
		w := b.words[i]
		for w != 0 {
			t := bits.TrailingZeros64(w)
			vals = append(vals, uint16(i<<6+t))
			w &= w - 1
		}
	}
	return &arrayContainer{vals: vals}
}

func (b *bitmapContainer) toRun() *runContainer {
	rc := &runContainer{}
	start, prev := -1, -2 // prev=-2: the first set bit always opens a run
	for i := 0; i < bitmapWords; i++ {
		w := b.words[i]
		for w != 0 {
			t := bits.TrailingZeros64(w)
			v := i<<6 + t
			if v != prev+1 {
				if start >= 0 {
					rc.iv = append(rc.iv, runInterval{start: uint16(start), length: uint16(prev - start)})
				}
				start = v
			}
			prev = v
			w &= w - 1
		}
	}
	if start >= 0 {
		rc.iv = append(rc.iv, runInterval{start: uint16(start), length: uint16(prev - start)})
	}
	return rc
}

func (b *bitmapContainer) serializedSize() int { return bitmapWords * 8 }

// ---------------------------------------------------------------------------
// runContainer: RLE pairs (start, length), best for contiguous ranges.
// ---------------------------------------------------------------------------

type runInterval struct{ start, length uint16 }

type runContainer struct{ iv []runInterval }

func (r *runContainer) typ() byte { return containerTypeRun }

func (r *runContainer) cardinality() int {
	n := 0
	for _, iv := range r.iv {
		n += int(iv.length) + 1
	}
	return n
}

func (r *runContainer) contains(v uint16) bool {
	i := sort.Search(len(r.iv), func(i int) bool { return r.iv[i].start >= v })
	if i < len(r.iv) && r.iv[i].start == v {
		return true
	}
	if i > 0 {
		iv := r.iv[i-1]
		return int(iv.start)+int(iv.length) >= int(v)
	}
	return false
}

func (r *runContainer) clone() container {
	iv := make([]runInterval, len(r.iv))
	copy(iv, r.iv)
	return &runContainer{iv: iv}
}

func (r *runContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{words: make([]uint64, bitmapWords)}
	card := 0
	for _, iv := range r.iv {
		setBitsRange(bc.words, int(iv.start), int(iv.start)+int(iv.length))
		card += int(iv.length) + 1
	}
	bc.card = card
	return bc
}

func (r *runContainer) toRun() *runContainer { return r }

func (r *runContainer) toArray() *arrayContainer {
	vals := make([]uint16, 0, r.cardinality())
	for _, iv := range r.iv {
		end := int(iv.start) + int(iv.length)
		for v := int(iv.start); v <= end; v++ {
			vals = append(vals, uint16(v))
		}
	}
	return &arrayContainer{vals: vals}
}

func (r *runContainer) serializedSize() int { return 4 + 4*len(r.iv) }

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func searchUint16s(s []uint16, v uint16) int {
	return sort.Search(len(s), func(i int) bool { return s[i] >= v })
}

// setBitsRange sets bits [lo, hi] (inclusive) in words.
func setBitsRange(words []uint64, lo, hi int) {
	first, last := lo>>6, hi>>6
	if first == last {
		words[first] |= (^uint64(0) << uint(lo&63)) & (^uint64(0) >> uint(63-(hi&63)))
		return
	}
	words[first] |= ^uint64(0) << uint(lo&63)
	for i := first + 1; i < last; i++ {
		words[i] = ^uint64(0)
	}
	words[last] |= ^uint64(0) >> uint(63-(hi&63))
}

func popcountWords(w []uint64) int {
	n := 0
	for _, x := range w {
		n += bits.OnesCount64(x)
	}
	return n
}

// repair picks the most compact container encoding for c.
// Returns nil when the container is empty.
func repair(c container) container {
	if c == nil {
		return nil
	}
	card := c.cardinality()
	if card == 0 {
		return nil
	}
	var runCount int
	switch t := c.(type) {
	case *runContainer:
		runCount = len(t.iv)
	case *arrayContainer:
		runCount = arrayRunCount(t)
	case *bitmapContainer:
		runCount = bitmapRunCount(t)
	}
	runBytes := 4 * runCount
	arrayBytes := 2 * card
	if card <= arrayMaxCardinality && arrayBytes <= runBytes {
		if ac, ok := c.(*arrayContainer); ok {
			return ac
		}
		return c.toArray()
	}
	if runBytes < bitmapWords*8 {
		if rc, ok := c.(*runContainer); ok {
			return rc
		}
		return c.toRun()
	}
	if bc, ok := c.(*bitmapContainer); ok {
		return bc
	}
	return c.toBitmap()
}

// arrayRunCount counts maximal runs of consecutive values in O(cardinality).
func arrayRunCount(a *arrayContainer) int {
	if len(a.vals) == 0 {
		return 0
	}
	n := 1
	prev := a.vals[0]
	for _, v := range a.vals[1:] {
		if v != prev+1 {
			n++
		}
		prev = v
	}
	return n
}

// bitmapRunCount counts maximal runs of consecutive set bits using word
// parallel operations (no per-bit scan).
func bitmapRunCount(b *bitmapContainer) int {
	n := 0
	prev := uint64(0)
	for _, w := range b.words {
		n += bits.OnesCount64(w &^ (w << 1))
		if w&1 != 0 && prev&(1<<63) != 0 {
			n-- // run continued across the word boundary
		}
		prev = w
	}
	return n
}
