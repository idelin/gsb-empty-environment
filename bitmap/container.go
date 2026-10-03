package bitmap

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxSize is the cardinality threshold above which an
	// arrayContainer is no longer the cheapest representation.
	arrayMaxSize = 4096
	// bitmapWordCount is the number of uint64 words in a bitmapContainer
	// (1024 words * 64 bits = 65536 bits = one full container key space).
	bitmapWordCount = 1024
	// bitmapBytes is the in-memory size of a bitmapContainer payload.
	bitmapBytes = bitmapWordCount * 8
	// runMaxCount is the number of RLE intervals above which a runContainer
	// is guaranteed to be worse than a bitmapContainer (4 bytes per run).
	runMaxCount = bitmapBytes / 4
)

// interval is an inclusive run [start, start+length].
type interval struct {
	start  uint16
	length uint16
}

func (iv interval) last() uint16 { return iv.start + iv.length }
func (iv interval) card() int    { return int(iv.length) + 1 }

// container stores the low 16 bits of a set of uint64 IDs that share the
// same high bits (the container key). Implementations must be treated as
// immutable by readers: writers always clone before mutating (copy-on-write)
// so a container pointer can be published atomically.
type container interface {
	contains(x uint16) bool
	cardinality() int
	clone() container
	// add returns the (possibly converted) container and whether x was new.
	add(x uint16) (container, bool)
	// remove returns the (possibly converted) container and whether x existed.
	remove(x uint16) (container, bool)
	forEach(fn func(x uint16))
	// optimize returns the cheapest equivalent representation.
	optimize() container
	runCount() int
}

// Representation kinds picked by chooseKind.
const (
	kindArray = iota
	kindBitmap
	kindRun
)

// chooseKind selects the cheapest representation for a container with the
// given cardinality and run count, comparing exact in-memory sizes.
func chooseKind(card, runs int) int {
	best := bitmapBytes
	kind := kindBitmap
	if card <= arrayMaxSize && card*2 <= best {
		best = card * 2
		kind = kindArray
	}
	if runs*4 < best {
		kind = kindRun
	}
	return kind
}

// ---------------------------------------------------------------------------
// arrayContainer: sorted []uint16, cheapest for sparse containers.
// ---------------------------------------------------------------------------

type arrayContainer struct {
	data []uint16 // sorted, deduplicated
}

func newArrayContainer() *arrayContainer {
	return &arrayContainer{data: make([]uint16, 0, 8)}
}

func (ac *arrayContainer) search(x uint16) (int, bool) {
	i := sort.Search(len(ac.data), func(i int) bool { return ac.data[i] >= x })
	return i, i < len(ac.data) && ac.data[i] == x
}

func (ac *arrayContainer) contains(x uint16) bool { _, ok := ac.search(x); return ok }
func (ac *arrayContainer) cardinality() int       { return len(ac.data) }

func (ac *arrayContainer) clone() container {
	d := make([]uint16, len(ac.data))
	copy(d, ac.data)
	return &arrayContainer{data: d}
}

func (ac *arrayContainer) add(x uint16) (container, bool) {
	i, ok := ac.search(x)
	if ok {
		return ac, false
	}
	ac.data = append(ac.data, 0)
	copy(ac.data[i+1:], ac.data[i:])
	ac.data[i] = x
	if len(ac.data) > arrayMaxSize {
		return ac.optimize(), true
	}
	return ac, true
}

func (ac *arrayContainer) remove(x uint16) (container, bool) {
	i, ok := ac.search(x)
	if !ok {
		return ac, false
	}
	ac.data = append(ac.data[:i], ac.data[i+1:]...)
	return ac, true
}

func (ac *arrayContainer) forEach(fn func(x uint16)) {
	for _, v := range ac.data {
		fn(v)
	}
}

func (ac *arrayContainer) runCount() int {
	if len(ac.data) == 0 {
		return 0
	}
	n := 1
	for i := 1; i < len(ac.data); i++ {
		if ac.data[i] != ac.data[i-1]+1 {
			n++
		}
	}
	return n
}

func (ac *arrayContainer) toRun() *runContainer {
	rc := &runContainer{}
	if len(ac.data) == 0 {
		return rc
	}
	start, prev := ac.data[0], ac.data[0]
	for _, v := range ac.data[1:] {
		if v == prev+1 {
			prev = v
			continue
		}
		rc.runs = append(rc.runs, interval{start, prev - start})
		start, prev = v, v
	}
	rc.runs = append(rc.runs, interval{start, prev - start})
	return rc
}

func (ac *arrayContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{card: len(ac.data)}
	for _, v := range ac.data {
		bc.words[v>>6] |= 1 << (v & 63)
	}
	return bc
}

func (ac *arrayContainer) optimize() container {
	switch chooseKind(len(ac.data), ac.runCount()) {
	case kindRun:
		return ac.toRun()
	case kindBitmap:
		return ac.toBitmap()
	}
	return ac
}

// ---------------------------------------------------------------------------
// bitmapContainer: fixed 65536-bit bitmap, cheapest for dense containers.
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	words [bitmapWordCount]uint64
	card  int
}

func (bc *bitmapContainer) contains(x uint16) bool {
	return bc.words[x>>6]&(1<<(x&63)) != 0
}

func (bc *bitmapContainer) cardinality() int { return bc.card }

func (bc *bitmapContainer) clone() container {
	return &bitmapContainer{words: bc.words, card: bc.card}
}

func (bc *bitmapContainer) add(x uint16) (container, bool) {
	mask := uint64(1) << (x & 63)
	w := &bc.words[x>>6]
	if *w&mask != 0 {
		return bc, false
	}
	*w |= mask
	bc.card++
	return bc, true
}

func (bc *bitmapContainer) remove(x uint16) (container, bool) {
	mask := uint64(1) << (x & 63)
	w := &bc.words[x>>6]
	if *w&mask == 0 {
		return bc, false
	}
	*w &^= mask
	bc.card--
	return bc, true
}

func (bc *bitmapContainer) forEach(fn func(x uint16)) {
	for i, w := range bc.words {
		for w != 0 {
			t := bits.TrailingZeros64(w)
			fn(uint16(i<<6 + t))
			w &= w - 1
		}
	}
}

// runCount counts 0->1 transitions across word boundaries.
func (bc *bitmapContainer) runCount() int {
	n := 0
	carry := uint64(0) // top bit of previous word, shifted into LSB
	for _, w := range bc.words {
		shifted := (w << 1) | carry
		n += bits.OnesCount64(w &^ shifted)
		carry = w >> 63
	}
	return n
}

func (bc *bitmapContainer) toArray() *arrayContainer {
	ac := &arrayContainer{data: make([]uint16, 0, bc.card)}
	bc.forEach(func(x uint16) { ac.data = append(ac.data, x) })
	return ac
}

func (bc *bitmapContainer) toRun() *runContainer { return bc.toArray().toRun() }

func (bc *bitmapContainer) optimize() container {
	switch chooseKind(bc.card, bc.runCount()) {
	case kindArray:
		return bc.toArray()
	case kindRun:
		return bc.toRun()
	}
	return bc
}

// recount recomputes the cached cardinality from the words.
func (bc *bitmapContainer) recount() {
	n := 0
	for _, w := range bc.words {
		n += bits.OnesCount64(w)
	}
	bc.card = n
}

// rangeMask returns a mask of bits [lo, hi] (inclusive, 0..63) without
// overflow at the word edges.
func rangeMask(lo, hi uint64) uint64 {
	return (^uint64(0) << lo) & (^uint64(0) >> (63 - hi))
}

// setRange sets or clears (via xor when flip=true) the bits in [s, l].
func (bc *bitmapContainer) setRange(s, l uint16, flip bool) {
	w0, w1 := int(s)>>6, int(l)>>6
	for wi := w0; wi <= w1; wi++ {
		var lo, hi uint64 = 0, 63
		if wi == w0 {
			lo = uint64(s & 63)
		}
		if wi == w1 {
			hi = uint64(l & 63)
		}
		mask := rangeMask(lo, hi)
		if flip {
			bc.words[wi] ^= mask
		} else {
			bc.words[wi] |= mask
		}
	}
}

// ---------------------------------------------------------------------------
// runContainer: RLE intervals, cheapest for clustered / sequential data.
// ---------------------------------------------------------------------------

type runContainer struct {
	runs []interval // sorted, non-overlapping, non-adjacent
}

func (rc *runContainer) cardinality() int {
	n := 0
	for _, iv := range rc.runs {
		n += iv.card()
	}
	return n
}

func (rc *runContainer) runCount() int { return len(rc.runs) }

func (rc *runContainer) contains(x uint16) bool {
	i := sort.Search(len(rc.runs), func(i int) bool { return rc.runs[i].last() >= x })
	return i < len(rc.runs) && rc.runs[i].start <= x
}

func (rc *runContainer) clone() container {
	r := make([]interval, len(rc.runs))
	copy(r, rc.runs)
	return &runContainer{runs: r}
}

func (rc *runContainer) add(x uint16) (container, bool) {
	// First run whose last >= x.
	i := sort.Search(len(rc.runs), func(i int) bool { return rc.runs[i].last() >= x })
	if i < len(rc.runs) {
		iv := rc.runs[i]
		if x >= iv.start {
			return rc, false // already present
		}
		if int(x)+1 == int(iv.start) {
			// x directly precedes run i: extend it left, maybe merge with i-1.
			if i > 0 && int(rc.runs[i-1].last())+1 == int(x) {
				rc.runs[i-1].length = rc.runs[i].last() - rc.runs[i-1].start
				rc.runs = append(rc.runs[:i], rc.runs[i+1:]...)
			} else {
				rc.runs[i].length = iv.last() - x
				rc.runs[i].start = x
			}
			return rc.afterAdd(), true
		}
		// x sits strictly before run i; maybe extend run i-1.
		if i > 0 && int(rc.runs[i-1].last())+1 == int(x) {
			rc.runs[i-1].length++
			return rc.afterAdd(), true
		}
		rc.runs = append(rc.runs, interval{})
		copy(rc.runs[i+1:], rc.runs[i:])
		rc.runs[i] = interval{start: x}
		return rc.afterAdd(), true
	}
	// x is beyond every run.
	if len(rc.runs) > 0 && int(rc.runs[len(rc.runs)-1].last())+1 == int(x) {
		rc.runs[len(rc.runs)-1].length++
		return rc.afterAdd(), true
	}
	rc.runs = append(rc.runs, interval{start: x})
	return rc.afterAdd(), true
}

func (rc *runContainer) afterAdd() container {
	if len(rc.runs) > runMaxCount {
		return rc.optimize()
	}
	return rc
}

func (rc *runContainer) remove(x uint16) (container, bool) {
	i := sort.Search(len(rc.runs), func(i int) bool { return rc.runs[i].last() >= x })
	if i >= len(rc.runs) || rc.runs[i].start > x {
		return rc, false
	}
	iv := rc.runs[i]
	switch {
	case iv.start == x && iv.last() == x:
		rc.runs = append(rc.runs[:i], rc.runs[i+1:]...)
	case iv.start == x:
		rc.runs[i].start++
		rc.runs[i].length--
	case iv.last() == x:
		rc.runs[i].length--
	default: // split the run around x
		right := interval{start: x + 1, length: iv.last() - (x + 1)}
		rc.runs[i].length = x - iv.start - 1
		rc.runs = append(rc.runs, interval{})
		copy(rc.runs[i+2:], rc.runs[i+1:])
		rc.runs[i+1] = right
	}
	return rc, true
}

func (rc *runContainer) forEach(fn func(x uint16)) {
	for _, iv := range rc.runs {
		for x := iv.start; ; x++ {
			fn(x)
			if x == iv.last() {
				break
			}
		}
	}
}

func (rc *runContainer) toArray() *arrayContainer {
	ac := &arrayContainer{data: make([]uint16, 0, rc.cardinality())}
	rc.forEach(func(x uint16) { ac.data = append(ac.data, x) })
	return ac
}

func (rc *runContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{}
	for _, iv := range rc.runs {
		bc.setRange(iv.start, iv.last(), false)
	}
	bc.card = rc.cardinality()
	return bc
}

func (rc *runContainer) optimize() container {
	switch chooseKind(rc.cardinality(), len(rc.runs)) {
	case kindArray:
		return rc.toArray()
	case kindBitmap:
		return rc.toBitmap()
	}
	return rc
}
