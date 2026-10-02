package roaring

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxSize is the cardinality threshold above which an
	// arrayContainer is automatically converted to a bitmapContainer.
	arrayMaxSize = 4096
	// bitmapWords is the number of uint64 words in a bitmapContainer
	// (1024 * 64 = 65536 bits, covering the low 16 bits of an ID).
	bitmapWords = 1024
)

// Container type identifiers used by the on-disk format.
const (
	typeArray  byte = 1
	typeBitmap byte = 2
	typeRun    byte = 3
)

// container stores the low 16 bits of a set of IDs that share the same
// high 48 bits. Implementations: arrayContainer (sparse), bitmapContainer
// (dense) and runContainer (contiguous runs, RLE).
type container interface {
	add(v uint16) container
	contains(v uint16) bool
	cardinality() int
	isFull() bool
	clone() container
	toBitmap() *bitmapContainer
	foreach(fn func(v uint16))
	typeID() byte
	sizeBytes() int
}

// ---------------------------------------------------------------------------
// arrayContainer: sorted slice, optimal for sparse data (<= 4096 values).
// ---------------------------------------------------------------------------

type arrayContainer struct{ data []uint16 }

func (a *arrayContainer) typeID() byte     { return typeArray }
func (a *arrayContainer) cardinality() int { return len(a.data) }
func (a *arrayContainer) sizeBytes() int   { return 2 * len(a.data) }
func (a *arrayContainer) isFull() bool     { return false }

func (a *arrayContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{}
	for _, v := range a.data {
		b.bits[v>>6] |= 1 << (v & 63)
	}
	b.card = len(a.data)
	return b
}

func (a *arrayContainer) contains(v uint16) bool {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	return i < len(a.data) && a.data[i] == v
}

func (a *arrayContainer) add(v uint16) container {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	if i < len(a.data) && a.data[i] == v {
		return a
	}
	a.data = append(a.data, 0)
	copy(a.data[i+1:], a.data[i:])
	a.data[i] = v
	if len(a.data) > arrayMaxSize {
		return a.toBitmap()
	}
	return a
}

func (a *arrayContainer) clone() container {
	d := make([]uint16, len(a.data))
	copy(d, a.data)
	return &arrayContainer{data: d}
}

func (a *arrayContainer) foreach(fn func(v uint16)) {
	for _, v := range a.data {
		fn(v)
	}
}

// ---------------------------------------------------------------------------
// bitmapContainer: fixed 8 KiB bitset, optimal for dense data.
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	bits [bitmapWords]uint64
	card int
}

func (b *bitmapContainer) typeID() byte               { return typeBitmap }
func (b *bitmapContainer) cardinality() int           { return b.card }
func (b *bitmapContainer) sizeBytes() int             { return 8 * bitmapWords }
func (b *bitmapContainer) isFull() bool               { return b.card == 1<<16 }
func (b *bitmapContainer) toBitmap() *bitmapContainer { return b }

func (b *bitmapContainer) contains(v uint16) bool {
	return b.bits[v>>6]&(1<<(v&63)) != 0
}

func (b *bitmapContainer) add(v uint16) container {
	m := uint64(1) << (v & 63)
	w := v >> 6
	if b.bits[w]&m == 0 {
		b.bits[w] |= m
		b.card++
	}
	return b
}

func (b *bitmapContainer) clone() container {
	return &bitmapContainer{bits: b.bits, card: b.card}
}

func (b *bitmapContainer) foreach(fn func(v uint16)) {
	for i := 0; i < bitmapWords; i++ {
		w := b.bits[i]
		for w != 0 {
			t := bits.TrailingZeros64(w)
			fn(uint16(i*64 + t))
			w &= w - 1
		}
	}
}

// ---------------------------------------------------------------------------
// runContainer: run-length encoded inclusive intervals, optimal for
// contiguous ranges.
// ---------------------------------------------------------------------------

type interval struct{ start, last uint16 }

type runContainer struct{ runs []interval }

func (r *runContainer) typeID() byte   { return typeRun }
func (r *runContainer) sizeBytes() int { return 4 * len(r.runs) }
func (r *runContainer) isFull() bool {
	return len(r.runs) == 1 && r.runs[0].start == 0 && r.runs[0].last == 0xffff
}

func (r *runContainer) cardinality() int {
	n := 0
	for _, iv := range r.runs {
		n += int(iv.last) - int(iv.start) + 1
	}
	return n
}

func (r *runContainer) contains(v uint16) bool {
	i := sort.Search(len(r.runs), func(i int) bool { return r.runs[i].last >= v })
	return i < len(r.runs) && r.runs[i].start <= v
}

func (r *runContainer) add(v uint16) container {
	n := len(r.runs)
	i := sort.Search(n, func(i int) bool { return uint32(r.runs[i].start) > uint32(v) })
	if i > 0 {
		prev := &r.runs[i-1]
		if v <= prev.last {
			return r
		}
		if uint32(v) == uint32(prev.last)+1 {
			prev.last = v
			if i < n && uint32(r.runs[i].start) == uint32(v)+1 {
				prev.last = r.runs[i].last
				r.runs = append(r.runs[:i], r.runs[i+1:]...)
			}
			return r
		}
	}
	if i < n && uint32(r.runs[i].start) == uint32(v)+1 {
		r.runs[i].start = v
		return r
	}
	r.runs = append(r.runs, interval{})
	copy(r.runs[i+1:], r.runs[i:])
	r.runs[i] = interval{v, v}
	return r
}

// addInterval merges the inclusive range [lo, hi] into the run list.
func (r *runContainer) addInterval(lo, hi uint16) {
	newStart, newLast := uint32(lo), uint32(hi)
	i := 0
	for i < len(r.runs) && uint32(r.runs[i].last)+1 < newStart {
		i++
	}
	j := i
	for j < len(r.runs) && uint32(r.runs[j].start) <= newLast+1 {
		if uint32(r.runs[j].start) < newStart {
			newStart = uint32(r.runs[j].start)
		}
		if uint32(r.runs[j].last) > newLast {
			newLast = uint32(r.runs[j].last)
		}
		j++
	}
	out := make([]interval, 0, len(r.runs)+1)
	out = append(out, r.runs[:i]...)
	out = append(out, interval{uint16(newStart), uint16(newLast)})
	out = append(out, r.runs[j:]...)
	r.runs = out
}

func (r *runContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{}
	card := 0
	for _, iv := range r.runs {
		for v := uint32(iv.start); v <= uint32(iv.last); v++ {
			b.bits[v>>6] |= 1 << (v & 63)
		}
		card += int(iv.last) - int(iv.start) + 1
	}
	b.card = card
	return b
}

func (r *runContainer) clone() container {
	runs := make([]interval, len(r.runs))
	copy(runs, r.runs)
	return &runContainer{runs: runs}
}

func (r *runContainer) foreach(fn func(v uint16)) {
	for _, iv := range r.runs {
		for v := uint32(iv.start); v <= uint32(iv.last); v++ {
			fn(uint16(v))
		}
	}
}

// ---------------------------------------------------------------------------
// conversions and container-level set operations
// ---------------------------------------------------------------------------

func containerToArray(c container) *arrayContainer {
	if a, ok := c.(*arrayContainer); ok {
		return a
	}
	out := &arrayContainer{data: make([]uint16, 0, c.cardinality())}
	c.foreach(func(v uint16) { out.data = append(out.data, v) })
	return out
}

func containerToRun(c container) *runContainer {
	if r, ok := c.(*runContainer); ok {
		return r
	}
	out := &runContainer{}
	c.foreach(func(v uint16) {
		n := len(out.runs)
		if n > 0 && uint32(v) == uint32(out.runs[n-1].last)+1 {
			out.runs[n-1].last = v
		} else {
			out.runs = append(out.runs, interval{v, v})
		}
	})
	return out
}

// repairBitmap demotes a bitmap container to an array container when its
// cardinality dropped below the array threshold.
func repairBitmap(b *bitmapContainer) container {
	if b.card <= arrayMaxSize {
		return containerToArray(b)
	}
	return b
}

// optimize converts a container to whichever representation is smallest.
func optimize(c container) container {
	card := c.cardinality()
	best := c
	bestSize := c.sizeBytes()
	if card <= arrayMaxSize && 2*card < bestSize {
		best = containerToArray(c)
		bestSize = 2 * card
	}
	r := containerToRun(c)
	if 4*len(r.runs) < bestSize {
		best = r
	}
	return best
}

// newRangeContainer builds the optimal container for the inclusive
// contiguous range [lo, hi]: a single RLE interval.
func newRangeContainer(lo, hi uint16) container {
	return &runContainer{runs: []interval{{lo, hi}}}
}

// addRangeToContainer returns a container with all values in [lo, hi] added.
func addRangeToContainer(c container, lo, hi uint16) container {
	if rc, ok := c.(*runContainer); ok {
		out := rc.clone().(*runContainer)
		out.addInterval(lo, hi)
		return out
	}
	bc := c.toBitmap()
	for v := uint32(lo); v <= uint32(hi); v++ {
		m := uint64(1) << (v & 63)
		w := v >> 6
		if bc.bits[w]&m == 0 {
			bc.bits[w] |= m
			bc.card++
		}
	}
	return repairBitmap(bc)
}

func intersectArrays(a, b *arrayContainer) container {
	out := &arrayContainer{}
	i, j := 0, 0
	for i < len(a.data) && j < len(b.data) {
		switch {
		case a.data[i] < b.data[j]:
			i++
		case a.data[i] > b.data[j]:
			j++
		default:
			out.data = append(out.data, a.data[i])
			i++
			j++
		}
	}
	return out
}

func filterArray(a *arrayContainer, o container) container {
	out := &arrayContainer{}
	for _, v := range a.data {
		if o.contains(v) {
			out.data = append(out.data, v)
		}
	}
	return out
}

func mergeArrays(a, b *arrayContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, len(a.data)+len(b.data))}
	i, j := 0, 0
	for i < len(a.data) && j < len(b.data) {
		switch {
		case a.data[i] < b.data[j]:
			out.data = append(out.data, a.data[i])
			i++
		case a.data[i] > b.data[j]:
			out.data = append(out.data, b.data[j])
			j++
		default:
			out.data = append(out.data, a.data[i])
			i++
			j++
		}
	}
	out.data = append(out.data, a.data[i:]...)
	out.data = append(out.data, b.data[j:]...)
	if len(out.data) > arrayMaxSize {
		return out.toBitmap()
	}
	return out
}

func xorArrays(a, b *arrayContainer) container {
	out := &arrayContainer{}
	i, j := 0, 0
	for i < len(a.data) && j < len(b.data) {
		switch {
		case a.data[i] < b.data[j]:
			out.data = append(out.data, a.data[i])
			i++
		case a.data[i] > b.data[j]:
			out.data = append(out.data, b.data[j])
			j++
		default:
			i++
			j++
		}
	}
	out.data = append(out.data, a.data[i:]...)
	out.data = append(out.data, b.data[j:]...)
	if len(out.data) > arrayMaxSize {
		return out.toBitmap()
	}
	return out
}

// bitmapBinOp applies a word-level boolean operation to two containers.
func bitmapBinOp(a, b container, op func(x, y uint64) uint64) container {
	ba, bb := a.toBitmap(), b.toBitmap()
	out := &bitmapContainer{}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := op(ba.bits[i], bb.bits[i])
		out.bits[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return repairBitmap(out)
}

func andContainers(a, b container) container {
	if a.isFull() {
		return b.clone()
	}
	if b.isFull() {
		return a.clone()
	}
	if aa, ok := a.(*arrayContainer); ok {
		if bb, ok := b.(*arrayContainer); ok {
			return intersectArrays(aa, bb)
		}
		return filterArray(aa, b)
	}
	if bb, ok := b.(*arrayContainer); ok {
		return filterArray(bb, a)
	}
	return bitmapBinOp(a, b, func(x, y uint64) uint64 { return x & y })
}

func orContainers(a, b container) container {
	if a.isFull() || b.isFull() {
		return &runContainer{runs: []interval{{0, 0xffff}}}
	}
	if aa, ok := a.(*arrayContainer); ok {
		if bb, ok := b.(*arrayContainer); ok {
			return mergeArrays(aa, bb)
		}
	}
	return bitmapBinOp(a, b, func(x, y uint64) uint64 { return x | y })
}

func xorContainers(a, b container) container {
	if a.isFull() && b.isFull() {
		return &arrayContainer{}
	}
	if aa, ok := a.(*arrayContainer); ok {
		if bb, ok := b.(*arrayContainer); ok {
			return xorArrays(aa, bb)
		}
	}
	return bitmapBinOp(a, b, func(x, y uint64) uint64 { return x ^ y })
}
