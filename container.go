package cbitmap

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxSize is the cardinality threshold above which an array
	// container is converted to a bitmap container.
	arrayMaxSize = 4096
	// bitmapWords is the number of uint64 words in a bitmap container
	// (65536 bits / 64).
	bitmapWords = 1024
	// bitmapBytes is the serialized size of a bitmap container.
	bitmapBytes = bitmapWords * 8

	arrayContainerID  = 1
	bitmapContainerID = 2
	runContainerID    = 3
)

// run16 is a half-open run [start, start+length] stored as start plus
// length-1, so a run can cover the full 0..65535 range.
type run16 struct {
	start  uint16
	length uint16 // number of values minus one
}

func (r run16) last() uint16 { return r.start + r.length }

// container is the internal interface implemented by arrayContainer,
// bitmapContainer and runContainer.
type container interface {
	contains(x uint16) bool
	cardinality() int
	clone() container
	// optimize returns the most compact encoding of the same set.
	optimize() container
	iterator() containerIterator
	typeID() int
	// dataSize is the serialized payload size in bytes.
	dataSize() int
}

// ---------------------------------------------------------------------------
// array container
// ---------------------------------------------------------------------------

type arrayContainer struct {
	data []uint16 // sorted, no duplicates
}

func (ac *arrayContainer) typeID() int      { return arrayContainerID }
func (ac *arrayContainer) cardinality() int { return len(ac.data) }
func (ac *arrayContainer) dataSize() int    { return 2 * len(ac.data) }

func (ac *arrayContainer) contains(x uint16) bool {
	i := sort.Search(len(ac.data), func(i int) bool { return ac.data[i] >= x })
	return i < len(ac.data) && ac.data[i] == x
}

func (ac *arrayContainer) clone() container {
	d := make([]uint16, len(ac.data))
	copy(d, ac.data)
	return &arrayContainer{data: d}
}

func (ac *arrayContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{words: make([]uint64, bitmapWords), card: len(ac.data)}
	for _, v := range ac.data {
		bc.words[v>>6] |= 1 << (v & 63)
	}
	return bc
}

func (ac *arrayContainer) toRun() *runContainer {
	runs := make([]run16, 0, 8)
	for i := 0; i < len(ac.data); {
		j := i
		for j+1 < len(ac.data) && ac.data[j+1] == ac.data[j]+1 {
			j++
		}
		runs = append(runs, run16{start: ac.data[i], length: ac.data[j] - ac.data[i]})
		i = j + 1
	}
	return &runContainer{runs: runs, card: len(ac.data)}
}

func (ac *arrayContainer) optimize() container {
	if len(ac.data) > arrayMaxSize {
		return ac.toBitmap().optimize()
	}
	runs := 1
	for i := 1; i < len(ac.data); i++ {
		if ac.data[i] != ac.data[i-1]+1 {
			runs++
		}
	}
	if len(ac.data) > 0 && 4*runs < 2*len(ac.data) {
		return ac.toRun()
	}
	return ac
}

func (ac *arrayContainer) iterator() containerIterator {
	return &arrayIterator{data: ac.data}
}

// ---------------------------------------------------------------------------
// bitmap container
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	words []uint64 // always len == bitmapWords
	card  int      // cached cardinality, -1 when unknown
}

func newBitmapContainer() *bitmapContainer {
	return &bitmapContainer{words: make([]uint64, bitmapWords), card: 0}
}

func (bc *bitmapContainer) typeID() int   { return bitmapContainerID }
func (bc *bitmapContainer) dataSize() int { return bitmapBytes }

// cardinality does not cache when card < 0 so that shared (zero-copy)
// containers remain safe for concurrent reads.
func (bc *bitmapContainer) cardinality() int {
	if bc.card >= 0 {
		return bc.card
	}
	c := 0
	for _, w := range bc.words {
		c += bits.OnesCount64(w)
	}
	return c
}

func (bc *bitmapContainer) contains(x uint16) bool {
	return bc.words[x>>6]&(1<<(x&63)) != 0
}

func (bc *bitmapContainer) clone() container {
	w := make([]uint64, bitmapWords)
	copy(w, bc.words)
	return &bitmapContainer{words: w, card: bc.card}
}

func (bc *bitmapContainer) toArray() *arrayContainer {
	card := bc.cardinality()
	out := make([]uint16, 0, card)
	for wi, w := range bc.words {
		for w != 0 {
			b := bits.TrailingZeros64(w)
			out = append(out, uint16(wi*64+b))
			w &= w - 1
		}
	}
	return &arrayContainer{data: out}
}

func (bc *bitmapContainer) toRun() *runContainer {
	runs := make([]run16, 0, 8)
	for wi, w := range bc.words {
		for w != 0 {
			b := bits.TrailingZeros64(w)
			start := wi*64 + b
			// number of consecutive one-bits starting at b;
			// TrailingZeros64 returns 64 for 0, so cap at the word end
			n := bits.TrailingZeros64(^w >> uint(b))
			if n > 64-b {
				n = 64 - b
			}
			runs = append(runs, run16{start: uint16(start), length: uint16(n - 1)})
			w &= ^(((uint64(1) << uint(n)) - 1) << uint(b))
		}
	}
	// merge runs that are adjacent across word boundaries
	merged := runs[:0]
	for _, r := range runs {
		if len(merged) > 0 && uint32(merged[len(merged)-1].last())+1 == uint32(r.start) {
			merged[len(merged)-1].length = r.last() - merged[len(merged)-1].start
		} else {
			merged = append(merged, r)
		}
	}
	return &runContainer{runs: merged, card: -1}
}

// countRuns counts the number of maximal runs of 1-bits across all words.
func countRuns(words []uint64) int {
	runs := 0
	prev := uint64(0)
	for _, w := range words {
		starts := w &^ ((w << 1) | (prev >> 63))
		runs += bits.OnesCount64(starts)
		prev = w
	}
	return runs
}

func (bc *bitmapContainer) optimize() container {
	return chooseBitmapEncoding(bc, bc.cardinality(), countRuns(bc.words))
}

// chooseBitmapEncoding picks the most compact encoding for a bitmap
// container whose cardinality and run count are already known.
func chooseBitmapEncoding(bc *bitmapContainer, card, runs int) container {
	best := bitmapBytes
	kind := bitmapContainerID
	if 2*card <= best {
		best = 2 * card
		kind = arrayContainerID
	}
	if 4*runs < best {
		kind = runContainerID
	}
	switch kind {
	case arrayContainerID:
		return bc.toArray()
	case runContainerID:
		return bc.toRun()
	}
	return bc
}

func (bc *bitmapContainer) iterator() containerIterator {
	return &bitmapIterator{words: bc.words}
}

// ---------------------------------------------------------------------------
// run container
// ---------------------------------------------------------------------------

type runContainer struct {
	runs []run16 // sorted, non-overlapping, non-adjacent
	card int     // cached cardinality, -1 when unknown
}

func (rc *runContainer) typeID() int   { return runContainerID }
func (rc *runContainer) dataSize() int { return 4 * len(rc.runs) }

func (rc *runContainer) cardinality() int {
	if rc.card >= 0 {
		return rc.card
	}
	c := 0
	for _, r := range rc.runs {
		c += int(r.length) + 1
	}
	return c
}

func (rc *runContainer) contains(x uint16) bool {
	i := sort.Search(len(rc.runs), func(i int) bool { return rc.runs[i].last() >= x })
	return i < len(rc.runs) && rc.runs[i].start <= x
}

func (rc *runContainer) clone() container {
	r := make([]run16, len(rc.runs))
	copy(r, rc.runs)
	return &runContainer{runs: r, card: rc.card}
}

func (rc *runContainer) toBitmap() *bitmapContainer {
	bc := newBitmapContainer()
	for _, r := range rc.runs {
		setRangeBits(bc.words, r.start, r.last())
	}
	bc.card = -1
	return bc
}

func (rc *runContainer) optimize() container {
	card := rc.cardinality()
	if card <= arrayMaxSize && 2*card < 4*len(rc.runs) {
		bc := rc.toBitmap()
		return bc.toArray()
	}
	if 4*len(rc.runs) > bitmapBytes && card > arrayMaxSize {
		return rc.toBitmap()
	}
	return rc
}

func (rc *runContainer) iterator() containerIterator {
	return &runIterator{runs: rc.runs}
}

// add inserts x into the run container, merging with adjacent runs.
// It reports whether the set changed.
func (rc *runContainer) add(x uint16) bool {
	runs := rc.runs
	i := sort.Search(len(runs), func(i int) bool { return runs[i].last() >= x })
	if i < len(runs) {
		r := runs[i]
		if x >= r.start {
			return false // already present
		}
		if uint32(x)+1 == uint32(r.start) {
			// extend run i to the left
			runs[i].start = x
			runs[i].length++
			if i > 0 && uint32(runs[i-1].last())+1 == uint32(x) {
				// x bridges runs i-1 and i: merge them
				runs[i-1].length = runs[i].last() - runs[i-1].start
				copy(runs[i:], runs[i+1:])
				rc.runs = runs[:len(runs)-1]
			}
			rc.card = -1
			return true
		}
		if i > 0 && uint32(runs[i-1].last())+1 == uint32(x) {
			// extend previous run to the right
			runs[i-1].length++
			rc.card = -1
			return true
		}
		// insert a new singleton run before i
		runs = append(runs, run16{})
		copy(runs[i+1:], runs[i:])
		runs[i] = run16{start: x}
		rc.runs = runs
		rc.card = -1
		return true
	}
	if len(runs) > 0 && uint32(runs[len(runs)-1].last())+1 == uint32(x) {
		runs[len(runs)-1].length++
		rc.card = -1
		return true
	}
	rc.runs = append(runs, run16{start: x})
	rc.card = -1
	return true
}

// ---------------------------------------------------------------------------
// shared bit helpers
// ---------------------------------------------------------------------------

// rangeMask returns word indices and bit masks covering [lo, hi].
func rangeMask(lo, hi uint16) (w0, w1 int, m0, m1 uint64) {
	w0 = int(lo >> 6)
	w1 = int(hi >> 6)
	m0 = ^uint64(0) << (lo & 63)
	m1 = ^uint64(0) >> (63 - (hi & 63))
	return
}

func setRangeBits(words []uint64, lo, hi uint16) {
	w0, w1, m0, m1 := rangeMask(lo, hi)
	if w0 == w1 {
		words[w0] |= m0 & m1
		return
	}
	words[w0] |= m0
	for w := w0 + 1; w < w1; w++ {
		words[w] = ^uint64(0)
	}
	words[w1] |= m1
}

func flipRangeBits(words []uint64, lo, hi uint16) {
	w0, w1, m0, m1 := rangeMask(lo, hi)
	if w0 == w1 {
		words[w0] ^= m0 & m1
		return
	}
	words[w0] ^= m0
	for w := w0 + 1; w < w1; w++ {
		words[w] ^= ^uint64(0)
	}
	words[w1] ^= m1
}

// ---------------------------------------------------------------------------
// container iterators
// ---------------------------------------------------------------------------

type containerIterator interface {
	next() (uint16, bool)
}

type arrayIterator struct {
	data []uint16
	i    int
}

func (it *arrayIterator) next() (uint16, bool) {
	if it.i >= len(it.data) {
		return 0, false
	}
	v := it.data[it.i]
	it.i++
	return v, true
}

type bitmapIterator struct {
	words []uint64
	wi    int
	cur   uint64
}

func (it *bitmapIterator) next() (uint16, bool) {
	for it.cur == 0 {
		if it.wi >= len(it.words) {
			return 0, false
		}
		it.cur = it.words[it.wi]
		it.wi++
	}
	b := bits.TrailingZeros64(it.cur)
	it.cur &= it.cur - 1
	return uint16((it.wi-1)*64 + b), true
}

type runIterator struct {
	runs []run16
	ri   int
	off  uint16
}

func (it *runIterator) next() (uint16, bool) {
	if it.ri >= len(it.runs) {
		return 0, false
	}
	r := it.runs[it.ri]
	v := r.start + it.off
	if it.off == r.length {
		it.ri++
		it.off = 0
	} else {
		it.off++
	}
	return v, true
}
