// Package cbitmap implements a compressed bitmap (Roaring-bitmap style)
// with three auto-converting container types and lock-free concurrent
// updates built on CAS operations.
package cbitmap

import "sort"

const (
	// arrayMaxSize is the cardinality threshold above which an
	// arrayContainer is converted to a bitmapContainer.
	arrayMaxSize = 4096
	// runMaxCount is the run-count threshold above which a runContainer
	// stops being space efficient versus an 8KiB bitmapContainer.
	runMaxCount = 2048
	// bitmapWords is the number of uint64 words in a 65536-bit bitmap.
	bitmapWords = 1024
)

const (
	typeArray byte = iota + 1
	typeBitmap
	typeRun
)

// interval is an inclusive [start, last] range of a run container.
type interval struct {
	start uint16
	last  uint16
}

// container stores the low 16 bits of a set of uint32 values that share
// the same high 16 bits. Containers are treated as immutable once
// published: every mutation returns a new container (copy-on-write) so
// that readers never need locks.
type container interface {
	cardinality() int
	contains(v uint16) bool
	// add returns the (possibly new) container and whether v was inserted.
	add(v uint16) (container, bool)
	ctype() byte
	clone() container
	// optimize converts the container to the most compact representation.
	optimize() container
	// toArray returns the sorted list of values.
	toArray() []uint16
}

// ---------------------------------------------------------------------------
// arrayContainer
// ---------------------------------------------------------------------------

type arrayContainer struct {
	data []uint16 // sorted, deduplicated
}

func newArrayContainer() *arrayContainer { return &arrayContainer{} }

func (a *arrayContainer) ctype() byte       { return typeArray }
func (a *arrayContainer) cardinality() int  { return len(a.data) }
func (a *arrayContainer) toArray() []uint16 { return append([]uint16(nil), a.data...) }

func (a *arrayContainer) clone() container {
	return &arrayContainer{data: append([]uint16(nil), a.data...)}
}

func (a *arrayContainer) contains(v uint16) bool {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	return i < len(a.data) && a.data[i] == v
}

func (a *arrayContainer) add(v uint16) (container, bool) {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	if i < len(a.data) && a.data[i] == v {
		return a, false
	}
	a.data = append(a.data, 0)
	copy(a.data[i+1:], a.data[i:])
	a.data[i] = v
	if len(a.data) > arrayMaxSize {
		return a.toBitmap(), true
	}
	return a, true
}

func (a *arrayContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{}
	for _, v := range a.data {
		b.bits[v>>6] |= 1 << (v & 63)
	}
	b.card = len(a.data)
	return b
}

// optimize switches to a run container when the RLE encoding is smaller.
func (a *arrayContainer) optimize() container {
	runs := countRunsOfSorted(a.data)
	if runs*4 < len(a.data)*2 {
		return runFromSorted(a.data)
	}
	return a
}

func countRunsOfSorted(s []uint16) int {
	if len(s) == 0 {
		return 0
	}
	runs := 1
	for i := 1; i < len(s); i++ {
		if s[i] != s[i-1]+1 {
			runs++
		}
	}
	return runs
}

func runFromSorted(s []uint16) *runContainer {
	rc := &runContainer{runs: make([]interval, 0, countRunsOfSorted(s))}
	for i, v := range s {
		if i > 0 && int(rc.runs[len(rc.runs)-1].last)+1 == int(v) {
			rc.runs[len(rc.runs)-1].last = v
		} else {
			rc.runs = append(rc.runs, interval{v, v})
		}
	}
	return rc
}

// ---------------------------------------------------------------------------
// bitmapContainer
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	bits [bitmapWords]uint64
	card int
}

func (b *bitmapContainer) ctype() byte      { return typeBitmap }
func (b *bitmapContainer) cardinality() int { return b.card }

func (b *bitmapContainer) clone() container {
	return &bitmapContainer{bits: b.bits, card: b.card}
}

func (b *bitmapContainer) contains(v uint16) bool {
	return b.bits[v>>6]&(1<<(v&63)) != 0
}

func (b *bitmapContainer) add(v uint16) (container, bool) {
	mask := uint64(1) << (v & 63)
	w := &b.bits[v>>6]
	if *w&mask != 0 {
		return b, false
	}
	*w |= mask
	b.card++
	return b, true
}

func (b *bitmapContainer) addAll(vals []uint16) {
	for _, v := range vals {
		mask := uint64(1) << (v & 63)
		w := &b.bits[v>>6]
		if *w&mask == 0 {
			*w |= mask
			b.card++
		}
	}
}

func (b *bitmapContainer) flipAll(vals []uint16) {
	for _, v := range vals {
		mask := uint64(1) << (v & 63)
		w := &b.bits[v>>6]
		if *w&mask == 0 {
			*w |= mask
			b.card++
		} else {
			*w &^= mask
			b.card--
		}
	}
}

func (b *bitmapContainer) toArray() []uint16 {
	out := make([]uint16, 0, b.card)
	for w := 0; w < bitmapWords; w++ {
		word := b.bits[w]
		for word != 0 {
			t := word & (-word)
			out = append(out, uint16(w*64+popcount64(t-1)))
			word ^= t
		}
	}
	return out
}

func popcount64(x uint64) int {
	x = x - ((x >> 1) & 0x5555555555555555)
	x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
	x = (x + (x >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((x * 0x0101010101010101) >> 56)
}

// runCount counts the number of maximal runs of set bits.
func (b *bitmapContainer) runCount() int {
	runs := 0
	prev := false
	for w := 0; w < bitmapWords; w++ {
		word := b.bits[w]
		for bit := 0; bit < 64; bit++ {
			set := word&(1<<bit) != 0
			if set && !prev {
				runs++
			}
			prev = set
		}
	}
	return runs
}

// optimize converts to array/run when they are more compact.
func (b *bitmapContainer) optimize() container {
	if b.card <= arrayMaxSize {
		return (&arrayContainer{data: b.toArray()}).optimize()
	}
	if b.runCount() <= runMaxCount {
		return runFromSorted(b.toArray())
	}
	return b
}

// ---------------------------------------------------------------------------
// runContainer
// ---------------------------------------------------------------------------

type runContainer struct {
	runs []interval // sorted, disjoint, non-adjacent
}

func (r *runContainer) ctype() byte { return typeRun }

func (r *runContainer) clone() container {
	return &runContainer{runs: append([]interval(nil), r.runs...)}
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

func (r *runContainer) add(v uint16) (container, bool) {
	iv := int(v)
	// First run whose last >= v-1 (adjacent-or-containing candidate).
	idx := sort.Search(len(r.runs), func(i int) bool { return int(r.runs[i].last) >= iv-1 })
	if idx < len(r.runs) {
		run := r.runs[idx]
		switch {
		case int(run.start) <= iv && iv <= int(run.last):
			return r, false // already present
		case int(run.start) == iv+1:
			// Extend left; possibly merge with the previous run.
			r.runs[idx].start = v
			if idx > 0 && int(r.runs[idx-1].last)+1 == iv {
				r.runs[idx-1].last = r.runs[idx].last
				r.runs = append(r.runs[:idx], r.runs[idx+1:]...)
			}
			return r.maybeConvert(), true
		case int(run.last) == iv-1:
			// Extend right; possibly merge with the next run.
			r.runs[idx].last = v
			if idx+1 < len(r.runs) && int(r.runs[idx+1].start) == iv+1 {
				r.runs[idx].last = r.runs[idx+1].last
				r.runs = append(r.runs[:idx+1], r.runs[idx+2:]...)
			}
			return r.maybeConvert(), true
		}
	}
	// Insert a new singleton run at idx.
	r.runs = append(r.runs, interval{})
	copy(r.runs[idx+1:], r.runs[idx:])
	r.runs[idx] = interval{v, v}
	return r.maybeConvert(), true
}

// maybeConvert switches representation when RLE is no longer compact.
func (r *runContainer) maybeConvert() container {
	card := r.cardinality()
	if card <= arrayMaxSize && card*2 <= len(r.runs)*4 {
		return &arrayContainer{data: r.toArray()}
	}
	if len(r.runs) > runMaxCount && card > arrayMaxSize {
		b := &bitmapContainer{card: card}
		for _, iv := range r.runs {
			for v := uint32(iv.start); v <= uint32(iv.last); v++ {
				b.bits[v>>6] |= 1 << (v & 63)
			}
		}
		return b
	}
	return r
}

func (r *runContainer) optimize() container { return r.maybeConvert() }

func (r *runContainer) toArray() []uint16 {
	out := make([]uint16, 0, r.cardinality())
	for _, iv := range r.runs {
		for v := uint32(iv.start); v <= uint32(iv.last); v++ {
			out = append(out, uint16(v))
		}
	}
	return out
}

// toBitmap materializes the runs into a bitmap container, filling whole
// uint64 words at a time for dense ranges.
func (r *runContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{card: r.cardinality()}
	for _, iv := range r.runs {
		s, e := int(iv.start), int(iv.last)
		w0, w1 := s>>6, e>>6
		if w0 == w1 {
			for v := s; v <= e; v++ {
				b.bits[w0] |= 1 << (uint(v) & 63)
			}
			continue
		}
		b.bits[w0] |= ^uint64(0) << (uint(s) & 63)
		for w := w0 + 1; w < w1; w++ {
			b.bits[w] = ^uint64(0)
		}
		b.bits[w1] |= ^uint64(0) >> (63 - (uint(e) & 63))
	}
	return b
}

// andRunRun intersects two run containers in O(len(a)+len(b)).
func andRunRun(a, b *runContainer) container {
	out := make([]interval, 0, min(len(a.runs), len(b.runs)))
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		s := max(int(a.runs[i].start), int(b.runs[j].start))
		e := min(int(a.runs[i].last), int(b.runs[j].last))
		if s <= e {
			out = append(out, interval{uint16(s), uint16(e)})
		}
		if a.runs[i].last < b.runs[j].last {
			i++
		} else {
			j++
		}
	}
	return (&runContainer{runs: out}).maybeConvert()
}

// orRunRun unions two run containers in O(len(a)+len(b)).
func orRunRun(a, b *runContainer) container {
	out := make([]interval, 0, len(a.runs)+len(b.runs))
	push := func(iv interval) {
		if n := len(out); n > 0 && int(out[n-1].last)+1 >= int(iv.start) {
			if iv.last > out[n-1].last {
				out[n-1].last = iv.last
			}
		} else {
			out = append(out, iv)
		}
	}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		if a.runs[i].start <= b.runs[j].start {
			push(a.runs[i])
			i++
		} else {
			push(b.runs[j])
			j++
		}
	}
	for ; i < len(a.runs); i++ {
		push(a.runs[i])
	}
	for ; j < len(b.runs); j++ {
		push(b.runs[j])
	}
	return (&runContainer{runs: out}).maybeConvert()
}

// xorRunRun computes the symmetric difference of two run containers via a
// parity sweep over interval boundary events, in O(len(a)+len(b)).
func xorRunRun(a, b *runContainer) container {
	out := make([]interval, 0, len(a.runs)+len(b.runs))
	i, j := 0, 0
	inA, inB := false, false
	cur := -1 // start of the currently open output run
	for i < len(a.runs) || j < len(b.runs) {
		next := 1 << 16
		if i < len(a.runs) {
			if inA {
				next = min(next, int(a.runs[i].last)+1)
			} else {
				next = min(next, int(a.runs[i].start))
			}
		}
		if j < len(b.runs) {
			if inB {
				next = min(next, int(b.runs[j].last)+1)
			} else {
				next = min(next, int(b.runs[j].start))
			}
		}
		before := inA != inB
		if i < len(a.runs) {
			if inA && int(a.runs[i].last)+1 == next {
				inA = false
				i++
			} else if !inA && int(a.runs[i].start) == next {
				inA = true
			}
		}
		if j < len(b.runs) {
			if inB && int(b.runs[j].last)+1 == next {
				inB = false
				j++
			} else if !inB && int(b.runs[j].start) == next {
				inB = true
			}
		}
		after := inA != inB
		if !before && after {
			cur = next
		} else if before && !after {
			out = append(out, interval{uint16(cur), uint16(next - 1)})
		}
	}
	return (&runContainer{runs: out}).maybeConvert()
}

// ---------------------------------------------------------------------------
// container-level boolean operations
// ---------------------------------------------------------------------------

func andContainers(a, b container) container {
	if ab, ok := a.(*bitmapContainer); ok {
		switch bb := b.(type) {
		case *bitmapContainer:
			return andBitmaps(ab, bb)
		case *runContainer:
			return andBitmaps(ab, bb.toBitmap())
		}
	} else if ra, ok := a.(*runContainer); ok {
		switch rb := b.(type) {
		case *runContainer:
			return andRunRun(ra, rb)
		case *bitmapContainer:
			return andBitmaps(ra.toBitmap(), rb)
		}
	}
	small, big := a, b
	if b.cardinality() < a.cardinality() {
		small, big = b, a
	}
	out := make([]uint16, 0, min(small.cardinality(), arrayMaxSize))
	for _, v := range small.toArray() {
		if big.contains(v) {
			out = append(out, v)
		}
	}
	return (&arrayContainer{data: out}).optimize()
}

func andBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := 0; i < bitmapWords; i++ {
		w := a.bits[i] & b.bits[i]
		out.bits[i] = w
		out.card += popcount64(w)
	}
	return out.optimize()
}

func orContainers(a, b container) container {
	if ab, ok := a.(*bitmapContainer); ok {
		switch bb := b.(type) {
		case *bitmapContainer:
			return orBitmaps(ab, bb)
		case *runContainer:
			return orBitmaps(ab, bb.toBitmap())
		}
	} else if ra, ok := a.(*runContainer); ok {
		switch rb := b.(type) {
		case *runContainer:
			return orRunRun(ra, rb)
		case *bitmapContainer:
			return orBitmaps(ra.toBitmap(), rb)
		}
	}
	if ab, ok := a.(*bitmapContainer); ok {
		out := ab.clone().(*bitmapContainer)
		out.addAll(b.toArray())
		return out.optimize()
	}
	if bb, ok := b.(*bitmapContainer); ok {
		out := bb.clone().(*bitmapContainer)
		out.addAll(a.toArray())
		return out.optimize()
	}
	return (&arrayContainer{data: mergeUnion(a.toArray(), b.toArray())}).optimize()
}

func orBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := 0; i < bitmapWords; i++ {
		w := a.bits[i] | b.bits[i]
		out.bits[i] = w
		out.card += popcount64(w)
	}
	return out.optimize()
}

func xorContainers(a, b container) container {
	if ab, ok := a.(*bitmapContainer); ok {
		switch bb := b.(type) {
		case *bitmapContainer:
			return xorBitmaps(ab, bb)
		case *runContainer:
			return xorBitmaps(ab, bb.toBitmap())
		}
	} else if ra, ok := a.(*runContainer); ok {
		switch rb := b.(type) {
		case *runContainer:
			return xorRunRun(ra, rb)
		case *bitmapContainer:
			return xorBitmaps(ra.toBitmap(), rb)
		}
	}
	if ab, ok := a.(*bitmapContainer); ok {
		out := ab.clone().(*bitmapContainer)
		out.flipAll(b.toArray())
		return out.optimize()
	}
	if bb, ok := b.(*bitmapContainer); ok {
		out := bb.clone().(*bitmapContainer)
		out.flipAll(a.toArray())
		return out.optimize()
	}
	return (&arrayContainer{data: mergeXor(a.toArray(), b.toArray())}).optimize()
}

func xorBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := 0; i < bitmapWords; i++ {
		w := a.bits[i] ^ b.bits[i]
		out.bits[i] = w
		out.card += popcount64(w)
	}
	return out.optimize()
}

func mergeUnion(x, y []uint16) []uint16 {
	out := make([]uint16, 0, len(x)+len(y))
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		switch {
		case x[i] < y[j]:
			out = append(out, x[i])
			i++
		case x[i] > y[j]:
			out = append(out, y[j])
			j++
		default:
			out = append(out, x[i])
			i++
			j++
		}
	}
	out = append(out, x[i:]...)
	out = append(out, y[j:]...)
	return out
}

func mergeXor(x, y []uint16) []uint16 {
	out := make([]uint16, 0, len(x)+len(y))
	i, j := 0, 0
	for i < len(x) && j < len(y) {
		switch {
		case x[i] < y[j]:
			out = append(out, x[i])
			i++
		case x[i] > y[j]:
			out = append(out, y[j])
			j++
		default:
			i++
			j++
		}
	}
	out = append(out, x[i:]...)
	out = append(out, y[j:]...)
	return out
}
