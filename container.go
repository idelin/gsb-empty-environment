package bitmap

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxSize is the cardinality threshold above which an
	// arrayContainer is converted into a bitmapContainer.
	arrayMaxSize = 4096
	// bitmapWords is the number of uint64 words in a bitmapContainer
	// (65536 bits = 8 KiB).
	bitmapWords = 1024
	// bitmapBytes is the memory footprint of a bitmapContainer.
	bitmapBytes = bitmapWords * 8
	// maxRunOptimize is the run-count early-exit limit: a container with
	// more runs than this is always cheaper as a bitmap.
	maxRunOptimize = bitmapBytes / 4
)

// container stores the low 16 bits of a set of uint32 keys that share the
// same high 16 bits. Implementations: arrayContainer, bitmapContainer,
// runContainer. Containers are never mutated in place by bitmap-level
// readers; writers mutate only while holding the owning shard lock.
type container interface {
	contains(x uint16) bool
	cardinality() int
	// add inserts x, returning the (possibly converted) container and
	// whether x was newly inserted.
	add(x uint16) (container, bool)
	// remove deletes x, returning the (possibly converted) container and
	// whether x was present.
	remove(x uint16) (container, bool)
	clone() container
}

// ---------------------------------------------------------------------------
// arrayContainer: sorted slice, <= arrayMaxSize elements.
// ---------------------------------------------------------------------------

type arrayContainer struct {
	data []uint16
}

func (a *arrayContainer) cardinality() int { return len(a.data) }

func (a *arrayContainer) search(x uint16) (int, bool) {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= x })
	return i, i < len(a.data) && a.data[i] == x
}

func (a *arrayContainer) contains(x uint16) bool {
	_, ok := a.search(x)
	return ok
}

func (a *arrayContainer) add(x uint16) (container, bool) {
	i, ok := a.search(x)
	if ok {
		return a, false
	}
	a.data = append(a.data, 0)
	copy(a.data[i+1:], a.data[i:])
	a.data[i] = x
	if len(a.data) > arrayMaxSize {
		return a.toBitmap(), true
	}
	return a, true
}

func (a *arrayContainer) remove(x uint16) (container, bool) {
	i, ok := a.search(x)
	if !ok {
		return a, false
	}
	a.data = append(a.data[:i], a.data[i+1:]...)
	return a, true
}

func (a *arrayContainer) clone() container {
	out := &arrayContainer{data: make([]uint16, len(a.data))}
	copy(out.data, a.data)
	return out
}

func (a *arrayContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{card: len(a.data)}
	for _, x := range a.data {
		b.words[x>>6] |= 1 << (x & 63)
	}
	return b
}

// addRange inserts every value in [start,last], converting to a bitmap
// container when the result would exceed arrayMaxSize.
func (a *arrayContainer) addRange(start, last uint16) container {
	n := int(last) - int(start) + 1
	if len(a.data)+n > arrayMaxSize {
		b := a.toBitmap()
		b.addRange(start, last)
		return b
	}
	lo := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= start })
	hi := sort.Search(len(a.data), func(i int) bool { return a.data[i] > last })
	existing := hi - lo
	out := make([]uint16, 0, len(a.data)+n-existing)
	out = append(out, a.data[:lo]...)
	for x := start; ; x++ {
		out = append(out, x)
		if x == last {
			break
		}
	}
	out = append(out, a.data[hi:]...)
	a.data = out
	return a
}

// ---------------------------------------------------------------------------
// bitmapContainer: fixed 8 KiB bitmap.
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	words [bitmapWords]uint64
	card  int
}

func (b *bitmapContainer) cardinality() int { return b.card }

func (b *bitmapContainer) contains(x uint16) bool {
	return b.words[x>>6]&(1<<(x&63)) != 0
}

func (b *bitmapContainer) add(x uint16) (container, bool) {
	mask := uint64(1) << (x & 63)
	w := &b.words[x>>6]
	if *w&mask != 0 {
		return b, false
	}
	*w |= mask
	b.card++
	return b, true
}

func (b *bitmapContainer) remove(x uint16) (container, bool) {
	mask := uint64(1) << (x & 63)
	w := &b.words[x>>6]
	if *w&mask == 0 {
		return b, false
	}
	*w &^= mask
	b.card--
	if b.card <= arrayMaxSize {
		return b.toArray(), true
	}
	return b, true
}

func (b *bitmapContainer) clone() container {
	out := &bitmapContainer{card: b.card}
	copy(out.words[:], b.words[:])
	return out
}

// addRange sets every bit in [start,last] and returns the number of newly
// set bits.
func (b *bitmapContainer) addRange(start, last uint16) int {
	firstWord := int(start) >> 6
	lastWord := int(last) >> 6
	added := 0
	for i := firstWord; i <= lastWord; i++ {
		lo := uint(0)
		hi := uint(63)
		if i == firstWord {
			lo = uint(start & 63)
		}
		if i == lastWord {
			hi = uint(last & 63)
		}
		mask := (^uint64(0) << lo) & (^uint64(0) >> (63 - hi))
		old := b.words[i]
		b.words[i] = old | mask
		added += bits.OnesCount64(mask &^ old)
	}
	b.card += added
	return added
}

func (b *bitmapContainer) toArray() *arrayContainer {
	out := &arrayContainer{data: make([]uint16, 0, b.card)}
	for i := 0; i < bitmapWords; i++ {
		w := b.words[i]
		for w != 0 {
			t := w & -w
			out.data = append(out.data, uint16(i*64+bits.TrailingZeros64(w)))
			w ^= t
		}
	}
	return out
}

// countRuns counts the number of maximal runs of consecutive set bits,
// exiting early once limit is exceeded.
func (b *bitmapContainer) countRuns(limit int) int {
	runs := 0
	carry := uint64(0)
	for i := 0; i < bitmapWords; i++ {
		w := b.words[i]
		shifted := (w << 1) | carry
		runs += bits.OnesCount64(w &^ shifted)
		carry = w >> 63
		if runs > limit {
			return runs
		}
	}
	return runs
}

func (b *bitmapContainer) toRun() *runContainer {
	r := &runContainer{}
	for i := 0; i < bitmapWords; i++ {
		w := b.words[i]
		for w != 0 {
			t := w & -w
			start := i*64 + bits.TrailingZeros64(w)
			// w+t propagates a carry through the lowest run of 1s,
			// leaving the bit just above the run as the lowest set bit.
			w2 := w + t
			last := i*64 + bits.TrailingZeros64(w2) - 1
			r.runs = append(r.runs, interval{uint16(start), uint16(last)})
			w &= w2
		}
	}
	return r
}

// ---------------------------------------------------------------------------
// runContainer: sorted, non-adjacent, non-overlapping inclusive intervals.
// ---------------------------------------------------------------------------

type interval struct {
	start uint16
	last  uint16 // inclusive
}

type runContainer struct {
	runs []interval
}

func (r *runContainer) cardinality() int {
	n := 0
	for _, iv := range r.runs {
		n += int(iv.last) - int(iv.start) + 1
	}
	return n
}

func (r *runContainer) contains(x uint16) bool {
	i := sort.Search(len(r.runs), func(i int) bool { return r.runs[i].last >= x })
	return i < len(r.runs) && r.runs[i].start <= x
}

func (r *runContainer) add(x uint16) (container, bool) {
	if r.contains(x) {
		return r, false
	}
	r.addRange(x, x)
	return r, true
}

func (r *runContainer) remove(x uint16) (container, bool) {
	i := sort.Search(len(r.runs), func(i int) bool { return r.runs[i].last >= x })
	if i == len(r.runs) || r.runs[i].start > x {
		return r, false
	}
	iv := r.runs[i]
	switch {
	case iv.start == x && iv.last == x:
		r.runs = append(r.runs[:i], r.runs[i+1:]...)
	case iv.start == x:
		r.runs[i].start = x + 1
	case iv.last == x:
		r.runs[i].last = x - 1
	default:
		r.runs[i].last = x - 1
		r.runs = append(r.runs, interval{})
		copy(r.runs[i+2:], r.runs[i+1:])
		r.runs[i+1] = interval{x + 1, iv.last}
	}
	return r, true
}

// addRange merges the inclusive interval [start,last] into the run list.
func (r *runContainer) addRange(start, last uint16) {
	i := sort.Search(len(r.runs), func(i int) bool {
		return uint32(r.runs[i].last)+1 >= uint32(start)
	})
	if i == len(r.runs) {
		r.runs = append(r.runs, interval{start, last})
		return
	}
	if uint32(r.runs[i].start) > uint32(last)+1 {
		// No overlap or adjacency: insert a new run at position i.
		r.runs = append(r.runs, interval{})
		copy(r.runs[i+1:], r.runs[i:])
		r.runs[i] = interval{start, last}
		return
	}
	newStart, newLast := start, last
	j := i
	for j < len(r.runs) && uint32(r.runs[j].start) <= uint32(newLast)+1 {
		if r.runs[j].start < newStart {
			newStart = r.runs[j].start
		}
		if r.runs[j].last > newLast {
			newLast = r.runs[j].last
		}
		j++
	}
	r.runs[i] = interval{newStart, newLast}
	r.runs = append(r.runs[:i+1], r.runs[j:]...)
}

func (r *runContainer) clone() container {
	out := &runContainer{runs: make([]interval, len(r.runs))}
	copy(out.runs, r.runs)
	return out
}

func (r *runContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{}
	for _, iv := range r.runs {
		b.addRange(iv.start, iv.last)
	}
	return b
}

func (r *runContainer) toArray() *arrayContainer {
	out := &arrayContainer{data: make([]uint16, 0, r.cardinality())}
	for _, iv := range r.runs {
		for x := iv.start; ; x++ {
			out.data = append(out.data, x)
			if x == iv.last {
				break
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// container optimization (automatic representation selection)
// ---------------------------------------------------------------------------

// optimize returns the most memory-efficient representation of c.
func optimize(c container) container {
	switch t := c.(type) {
	case *arrayContainer:
		if len(t.data) > arrayMaxSize {
			return t.toBitmap()
		}
		return t
	case *bitmapContainer:
		return optimizeBitmap(t)
	case *runContainer:
		return optimizeRun(t)
	}
	return c
}

func optimizeBitmap(b *bitmapContainer) container {
	if b.card == 0 {
		return &arrayContainer{}
	}
	if b.card <= arrayMaxSize {
		return b.toArray()
	}
	if runs := b.countRuns(maxRunOptimize); runs <= maxRunOptimize {
		return b.toRun()
	}
	return b
}

func optimizeRun(r *runContainer) container {
	card := r.cardinality()
	if card == 0 {
		return &arrayContainer{}
	}
	runBytes := 4 * len(r.runs)
	if card <= arrayMaxSize && 2*card <= runBytes {
		return r.toArray()
	}
	if runBytes >= bitmapBytes {
		return r.toBitmap()
	}
	return r
}

// containerAddRange inserts [start,last] into c, returning the optimized
// (possibly converted) container.
func containerAddRange(c container, start, last uint16) container {
	switch t := c.(type) {
	case *arrayContainer:
		return optimize(t.addRange(start, last))
	case *bitmapContainer:
		t.addRange(start, last)
		return optimizeBitmap(t)
	case *runContainer:
		t.addRange(start, last)
		return optimizeRun(t)
	}
	return c
}
