// Package bitmap implements a compressed bitmap (roaring-style) with
// Array, Bitmap and Run-Length-Encoded containers, automatic container
// conversion and fast AND/OR/XOR set algebra.
package bitmap

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxSize is the cardinality threshold above which an
	// ArrayContainer is converted into a BitmapContainer.
	arrayMaxSize = 4096
	// containerBits is the number of values stored per container
	// (the low 16 bits of a uint64 id).
	containerBits = 1 << 16
	wordCount     = containerBits / 64
)

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

func fnvByte(h uint64, b byte) uint64 { return (h ^ uint64(b)) * fnvPrime64 }

func fnv16(h uint64, v uint16) uint64 {
	h = fnvByte(h, byte(v))
	return fnvByte(h, byte(v>>8))
}

func fnv64(h uint64, v uint64) uint64 {
	for i := 0; i < 8; i++ {
		h = fnvByte(h, byte(v))
		v >>= 8
	}
	return h
}

// container stores the low 16 bits of a set of ids.
type container interface {
	contains(v uint16) bool
	cardinality() int
	// add inserts v and returns the (possibly converted) container.
	add(v uint16) container
	clone() container
	sizeInBytes() int
	runCount() int
	toArray() *arrayContainer
	toBitmap() *bitmapContainer
	toRun() *runContainer
	foreach(f func(uint16))
	typeTag() byte
	hash(h uint64) uint64
}

// optimize returns the most compact representation of c.
func optimize(c container) container {
	card := c.cardinality()
	best := c
	bestSize := c.sizeInBytes()
	if card <= arrayMaxSize {
		if sz := 2 * card; sz < bestSize {
			best = c.toArray()
			bestSize = sz
		}
	}
	if rc := c.runCount(); rc > 0 {
		if sz := 4 * rc; sz < bestSize {
			best = c.toRun()
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// ArrayContainer
// ---------------------------------------------------------------------------

type arrayContainer struct {
	data []uint16 // sorted, unique
}

func (ac *arrayContainer) contains(v uint16) bool {
	i := sort.Search(len(ac.data), func(i int) bool { return ac.data[i] >= v })
	return i < len(ac.data) && ac.data[i] == v
}

func (ac *arrayContainer) cardinality() int { return len(ac.data) }

func (ac *arrayContainer) add(v uint16) container {
	i := sort.Search(len(ac.data), func(i int) bool { return ac.data[i] >= v })
	if i < len(ac.data) && ac.data[i] == v {
		return ac
	}
	ac.data = append(ac.data, 0)
	copy(ac.data[i+1:], ac.data[i:])
	ac.data[i] = v
	if len(ac.data) > arrayMaxSize {
		return ac.toBitmap()
	}
	return ac
}

func (ac *arrayContainer) clone() container {
	d := make([]uint16, len(ac.data))
	copy(d, ac.data)
	return &arrayContainer{data: d}
}

func (ac *arrayContainer) sizeInBytes() int { return 2 * len(ac.data) }

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

func (ac *arrayContainer) toArray() *arrayContainer { return ac }

func (ac *arrayContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{card: len(ac.data)}
	for _, v := range ac.data {
		bc.words[v>>6] |= 1 << (v & 63)
	}
	return bc
}

func (ac *arrayContainer) toRun() *runContainer {
	rc := &runContainer{}
	if len(ac.data) == 0 {
		return rc
	}
	start, prev := ac.data[0], ac.data[0]
	for _, v := range ac.data[1:] {
		if v != prev+1 {
			rc.runs = append(rc.runs, interval{start, prev})
			start = v
		}
		prev = v
	}
	rc.runs = append(rc.runs, interval{start, prev})
	return rc
}

func (ac *arrayContainer) foreach(f func(uint16)) {
	for _, v := range ac.data {
		f(v)
	}
}

func (ac *arrayContainer) typeTag() byte { return 'a' }

func (ac *arrayContainer) hash(h uint64) uint64 {
	for _, v := range ac.data {
		h = fnv16(h, v)
	}
	return h
}

// ---------------------------------------------------------------------------
// BitmapContainer
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	words [wordCount]uint64
	card  int
}

func (bc *bitmapContainer) contains(v uint16) bool {
	return bc.words[v>>6]&(1<<(v&63)) != 0
}

func (bc *bitmapContainer) cardinality() int { return bc.card }

func (bc *bitmapContainer) add(v uint16) container {
	m := uint64(1) << (v & 63)
	if bc.words[v>>6]&m == 0 {
		bc.words[v>>6] |= m
		bc.card++
	}
	return bc
}

func (bc *bitmapContainer) clone() container {
	c := *bc
	return &c
}

func (bc *bitmapContainer) sizeInBytes() int { return 8 * wordCount }

func (bc *bitmapContainer) runCount() int {
	runs := 0
	var prev uint64
	for _, w := range bc.words {
		shifted := (w << 1) | (prev >> 63)
		runs += bits.OnesCount64(w &^ shifted)
		prev = w
	}
	return runs
}

func (bc *bitmapContainer) recount() {
	c := 0
	for _, w := range bc.words {
		c += bits.OnesCount64(w)
	}
	bc.card = c
}

// setRange sets bits [lo, hi] (inclusive).
func (bc *bitmapContainer) setRange(lo, hi uint16) {
	wlo, whi := lo>>6, hi>>6
	maskLo := ^uint64(0) << (lo & 63)
	maskHi := ^uint64(0) >> (63 - (hi & 63))
	if wlo == whi {
		bc.words[wlo] |= maskLo & maskHi
		return
	}
	bc.words[wlo] |= maskLo
	for w := wlo + 1; w < whi; w++ {
		bc.words[w] = ^uint64(0)
	}
	bc.words[whi] |= maskHi
}

// flipRange toggles bits [lo, hi] (inclusive).
func (bc *bitmapContainer) flipRange(lo, hi uint16) {
	wlo, whi := lo>>6, hi>>6
	maskLo := ^uint64(0) << (lo & 63)
	maskHi := ^uint64(0) >> (63 - (hi & 63))
	if wlo == whi {
		bc.words[wlo] ^= maskLo & maskHi
		return
	}
	bc.words[wlo] ^= maskLo
	for w := wlo + 1; w < whi; w++ {
		bc.words[w] ^= ^uint64(0)
	}
	bc.words[whi] ^= maskHi
}

func (bc *bitmapContainer) toArray() *arrayContainer {
	d := make([]uint16, 0, bc.card)
	for i, w := range bc.words {
		for w != 0 {
			t := bits.TrailingZeros64(w)
			d = append(d, uint16(i*64+t))
			w &= w - 1
		}
	}
	return &arrayContainer{data: d}
}

func (bc *bitmapContainer) toBitmap() *bitmapContainer { return bc }

func (bc *bitmapContainer) toRun() *runContainer { return bc.toArray().toRun() }

func (bc *bitmapContainer) foreach(f func(uint16)) {
	for i, w := range bc.words {
		for w != 0 {
			t := bits.TrailingZeros64(w)
			f(uint16(i*64 + t))
			w &= w - 1
		}
	}
}

func (bc *bitmapContainer) typeTag() byte { return 'b' }

func (bc *bitmapContainer) hash(h uint64) uint64 {
	for _, w := range bc.words {
		h = fnv64(h, w)
	}
	return h
}

// ---------------------------------------------------------------------------
// RunContainer (RLE)
// ---------------------------------------------------------------------------

type interval struct {
	start, last uint16 // inclusive
}

type runContainer struct {
	runs []interval // sorted, disjoint, non-adjacent
}

func (rc *runContainer) contains(v uint16) bool {
	i := sort.Search(len(rc.runs), func(i int) bool { return rc.runs[i].start > v }) - 1
	return i >= 0 && rc.runs[i].last >= v
}

func (rc *runContainer) cardinality() int {
	n := 0
	for _, r := range rc.runs {
		n += int(r.last) - int(r.start) + 1
	}
	return n
}

func (rc *runContainer) add(v uint16) container {
	i := sort.Search(len(rc.runs), func(k int) bool { return int(rc.runs[k].last)+1 >= int(v) })
	if i < len(rc.runs) {
		if int(rc.runs[i].start) <= int(v) {
			if int(v) <= int(rc.runs[i].last) {
				return rc // already present
			}
			// v == last+1: extend run to the right, merging the next run.
			rc.runs[i].last = v
			if i+1 < len(rc.runs) && int(rc.runs[i+1].start) <= int(v)+1 {
				rc.runs[i].last = rc.runs[i+1].last
				rc.runs = append(rc.runs[:i+1], rc.runs[i+2:]...)
			}
			return rc
		}
		if int(rc.runs[i].start) == int(v)+1 {
			rc.runs[i].start = v
			return rc
		}
	}
	rc.runs = append(rc.runs, interval{})
	copy(rc.runs[i+1:], rc.runs[i:])
	rc.runs[i] = interval{v, v}
	return rc
}

func (rc *runContainer) clone() container {
	r := make([]interval, len(rc.runs))
	copy(r, rc.runs)
	return &runContainer{runs: r}
}

func (rc *runContainer) sizeInBytes() int { return 4 * len(rc.runs) }

func (rc *runContainer) runCount() int { return len(rc.runs) }

func (rc *runContainer) toArray() *arrayContainer {
	d := make([]uint16, 0, rc.cardinality())
	for _, r := range rc.runs {
		for v := r.start; ; v++ {
			d = append(d, v)
			if v == r.last {
				break
			}
		}
	}
	return &arrayContainer{data: d}
}

func (rc *runContainer) toBitmap() *bitmapContainer {
	bc := &bitmapContainer{}
	for _, r := range rc.runs {
		bc.setRange(r.start, r.last)
	}
	bc.recount()
	return bc
}

func (rc *runContainer) toRun() *runContainer { return rc }

func (rc *runContainer) foreach(f func(uint16)) {
	for _, r := range rc.runs {
		for v := r.start; ; v++ {
			f(v)
			if v == r.last {
				break
			}
		}
	}
}

func (rc *runContainer) typeTag() byte { return 'r' }

func (rc *runContainer) hash(h uint64) uint64 {
	for _, r := range rc.runs {
		h = fnv16(h, r.start)
		h = fnv16(h, r.last)
	}
	return h
}
