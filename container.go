package roaring

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxSize is the cardinality threshold above which an
	// arrayContainer is converted to a run or bitmap container.
	arrayMaxSize = 4096
	// bitmapWordCount is the number of uint64 words in a bitmapContainer
	// (65536 bits = 8 KiB).
	bitmapWordCount = 1024
	// runMaxCount is the maximum number of runs kept in a runContainer;
	// beyond this a bitmap container is more compact (4*runs >= 8192).
	runMaxCount = 2048
)

// interval16 is an inclusive [start, last] range of 16-bit values.
type interval16 struct {
	start, last uint16
}

// container stores the low 16 bits of a set of uint32 keys that share the
// same high 16 bits. Implementations: arrayContainer, bitmapContainer,
// runContainer.
type container interface {
	cardinality() int
	contains(x uint16) bool
	// add inserts x and returns the container to store (may convert to a
	// different container type).
	add(x uint16) container
	foreach(fn func(x uint16))
	clone() container
	typeID() byte
	writeTo(w *byteWriter)
	toSortedSlice() []uint16
}

// ---- arrayContainer: sorted slice, best for sparse sets ----

type arrayContainer struct {
	vals []uint16
}

func (a *arrayContainer) cardinality() int { return len(a.vals) }

func (a *arrayContainer) contains(x uint16) bool {
	i := sort.Search(len(a.vals), func(i int) bool { return a.vals[i] >= x })
	return i < len(a.vals) && a.vals[i] == x
}

func (a *arrayContainer) add(x uint16) container {
	i := sort.Search(len(a.vals), func(i int) bool { return a.vals[i] >= x })
	if i < len(a.vals) && a.vals[i] == x {
		return a
	}
	a.vals = append(a.vals, 0)
	copy(a.vals[i+1:], a.vals[i:])
	a.vals[i] = x
	if len(a.vals) > arrayMaxSize {
		return convertFromSorted(a.vals)
	}
	return a
}

func (a *arrayContainer) foreach(fn func(x uint16)) {
	for _, v := range a.vals {
		fn(v)
	}
}

func (a *arrayContainer) clone() container {
	vals := make([]uint16, len(a.vals))
	copy(vals, a.vals)
	return &arrayContainer{vals: vals}
}

func (a *arrayContainer) typeID() byte { return 0 }

func (a *arrayContainer) writeTo(w *byteWriter) {
	w.u32(uint32(len(a.vals)))
	for _, v := range a.vals {
		w.u16(v)
	}
}

func (a *arrayContainer) toSortedSlice() []uint16 { return a.vals }

// ---- bitmapContainer: 8 KiB bitset, best for dense random sets ----

type bitmapContainer struct {
	words [bitmapWordCount]uint64
	card  int
}

func (b *bitmapContainer) cardinality() int { return b.card }

func (b *bitmapContainer) contains(x uint16) bool {
	return b.words[x>>6]&(uint64(1)<<(x&63)) != 0
}

func (b *bitmapContainer) add(x uint16) container {
	mask := uint64(1) << (x & 63)
	w := &b.words[x>>6]
	if *w&mask == 0 {
		*w |= mask
		b.card++
	}
	return b
}

func (b *bitmapContainer) foreach(fn func(x uint16)) {
	for wi, word := range b.words {
		for word != 0 {
			t := bits.TrailingZeros64(word)
			fn(uint16(wi<<6 + t))
			word &= word - 1
		}
	}
}

func (b *bitmapContainer) clone() container {
	c := *b
	return &c
}

func (b *bitmapContainer) typeID() byte { return 1 }

func (b *bitmapContainer) writeTo(w *byteWriter) {
	w.u32(uint32(b.card))
	for _, word := range b.words {
		w.u64(word)
	}
}

func (b *bitmapContainer) toSortedSlice() []uint16 {
	out := make([]uint16, 0, b.card)
	b.foreach(func(x uint16) { out = append(out, x) })
	return out
}

func (b *bitmapContainer) runs() []interval16 {
	var runs []interval16
	start, prev := -1, -1
	b.foreach(func(x uint16) {
		if start >= 0 && int(x) == prev+1 {
			prev = int(x)
			return
		}
		if start >= 0 {
			runs = append(runs, interval16{uint16(start), uint16(prev)})
		}
		start, prev = int(x), int(x)
	})
	if start >= 0 {
		runs = append(runs, interval16{uint16(start), uint16(prev)})
	}
	return runs
}

// optimizeBitmap converts b to a run or array container when that is
// strictly more compact.
func optimizeBitmap(b *bitmapContainer) container {
	runs := b.runs()
	if len(runs) < runMaxCount && len(runs)*4 < b.card*2 {
		return &runContainer{runs: runs}
	}
	if b.card <= arrayMaxSize {
		return &arrayContainer{vals: b.toSortedSlice()}
	}
	return b
}

// ---- runContainer: RLE intervals, best for dense contiguous ranges ----

type runContainer struct {
	runs []interval16
}

func (r *runContainer) cardinality() int {
	n := 0
	for _, iv := range r.runs {
		n += int(iv.last) - int(iv.start) + 1
	}
	return n
}

func (r *runContainer) contains(x uint16) bool {
	i := sort.Search(len(r.runs), func(i int) bool { return r.runs[i].start > x })
	return i > 0 && r.runs[i-1].last >= x
}

func (r *runContainer) add(x uint16) container {
	runs := r.runs
	i := sort.Search(len(runs), func(i int) bool { return runs[i].start > x })
	switch {
	case i < len(runs) && uint32(runs[i].start) == uint32(x)+1:
		// x is immediately before runs[i]; merge with previous run if adjacent.
		if i > 0 && uint32(runs[i-1].last)+1 >= uint32(x) {
			runs[i-1].last = runs[i].last
			r.runs = append(runs[:i], runs[i+1:]...)
		} else {
			runs[i].start = x
		}
	case i > 0 && uint32(runs[i-1].last)+1 >= uint32(x):
		// x is inside or immediately after runs[i-1].
		if runs[i-1].last < x {
			runs[i-1].last = x
		}
	default:
		r.runs = append(r.runs, interval16{})
		copy(r.runs[i+1:], r.runs[i:])
		r.runs[i] = interval16{start: x, last: x}
	}
	if len(r.runs) >= runMaxCount {
		return convertFromSorted(r.toSortedSlice())
	}
	return r
}

func (r *runContainer) foreach(fn func(x uint16)) {
	for _, iv := range r.runs {
		for v := uint32(iv.start); v <= uint32(iv.last); v++ {
			fn(uint16(v))
		}
	}
}

func (r *runContainer) clone() container {
	runs := make([]interval16, len(r.runs))
	copy(runs, r.runs)
	return &runContainer{runs: runs}
}

func (r *runContainer) typeID() byte { return 2 }

func (r *runContainer) writeTo(w *byteWriter) {
	w.u32(uint32(len(r.runs)))
	for _, iv := range r.runs {
		w.u16(iv.start)
		w.u16(iv.last)
	}
}

func (r *runContainer) toSortedSlice() []uint16 {
	out := make([]uint16, 0, r.cardinality())
	r.foreach(func(x uint16) { out = append(out, x) })
	return out
}

// ---- shared helpers ----

func runsFromSorted(vals []uint16) []interval16 {
	var runs []interval16
	for i, v := range vals {
		if i > 0 && uint32(v) == uint32(vals[i-1])+1 {
			runs[len(runs)-1].last = v
		} else {
			runs = append(runs, interval16{start: v, last: v})
		}
	}
	return runs
}

func bitmapFromSorted(vals []uint16) *bitmapContainer {
	b := &bitmapContainer{card: len(vals)}
	for _, v := range vals {
		b.words[v>>6] |= uint64(1) << (v & 63)
	}
	return b
}

// convertFromSorted picks the most compact container for a sorted slice of
// distinct values.
func convertFromSorted(vals []uint16) container {
	if len(vals) == 0 {
		return &arrayContainer{}
	}
	runs := runsFromSorted(vals)
	if len(vals) <= arrayMaxSize {
		if len(runs)*2 < len(vals) {
			return &runContainer{runs: runs}
		}
		return &arrayContainer{vals: vals}
	}
	if len(runs) < runMaxCount {
		return &runContainer{runs: runs}
	}
	return bitmapFromSorted(vals)
}
