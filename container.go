package bitmapckpt

import (
	"io"
	"math/bits"
)

const (
	// arrayMaxSize is the cardinality threshold above which an array
	// container is converted into a bitmap container (roaring default).
	arrayMaxSize = 4096
	// bitmapWords is the number of uint64 words in a bitmap container.
	bitmapWords = 1024
	// containerSize is the number of values one container covers (2^16).
	containerSize = 1 << 16
	// runMaxCount is the run-count threshold above which a run container
	// is converted into a bitmap container (2048 runs * 4B == 8KB bitmap).
	runMaxCount = bitmapWords * 2
)

type containerType uint8

const (
	typeArray containerType = iota
	typeBitmap
	typeRun
)

// container stores the low 16 bits of a set of uint32 values.
// Containers are copy-on-write: once shared (referenced by a snapshot,
// checkpoint or another bitmap) they are never mutated in place.
type container interface {
	add(v uint16) container
	addRange(lo, hi uint16) container
	contains(v uint16) bool
	cardinality() int
	clone() container
	isShared() bool
	setShared(bool)
	foreach(fn func(uint16) bool)
	writeTo(w io.Writer) error
	typeID() containerType
}

type containerBase struct{ shared bool }

func (c *containerBase) isShared() bool   { return c.shared }
func (c *containerBase) setShared(v bool) { c.shared = v }

// ---------------------------------------------------------------------------
// array container: sorted slice of uint16, cardinality <= arrayMaxSize
// ---------------------------------------------------------------------------

type arrayContainer struct {
	containerBase
	data []uint16
}

func newArrayContainer() *arrayContainer {
	return &arrayContainer{data: make([]uint16, 0, 4)}
}

func (a *arrayContainer) typeID() containerType { return typeArray }
func (a *arrayContainer) cardinality() int      { return len(a.data) }

func lowerBound(data []uint16, v uint32) int {
	lo, hi := 0, len(data)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if uint32(data[mid]) < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (a *arrayContainer) contains(v uint16) bool {
	i := lowerBound(a.data, uint32(v))
	return i < len(a.data) && a.data[i] == v
}

func (a *arrayContainer) add(v uint16) container {
	i := lowerBound(a.data, uint32(v))
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

func (a *arrayContainer) addRange(lo, hi uint16) container {
	if lo > hi {
		return a
	}
	s := lowerBound(a.data, uint32(lo))
	e := lowerBound(a.data, uint32(hi)+1)
	rangeLen := int(hi) - int(lo) + 1
	newCard := len(a.data) - (e - s) + rangeLen
	if newCard > arrayMaxSize {
		b := a.toBitmap()
		return b.addRange(lo, hi)
	}
	out := make([]uint16, 0, newCard)
	out = append(out, a.data[:s]...)
	for v := uint32(lo); v <= uint32(hi); v++ {
		out = append(out, uint16(v))
	}
	out = append(out, a.data[e:]...)
	a.data = out
	return a
}

func (a *arrayContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{}
	for _, v := range a.data {
		b.words[v>>6] |= uint64(1) << (v & 63)
	}
	b.card = len(a.data)
	return b
}

func (a *arrayContainer) toRun() *runContainer {
	r := &runContainer{card: len(a.data)}
	if len(a.data) == 0 {
		return r
	}
	start, prev := uint32(a.data[0]), uint32(a.data[0])
	for _, v := range a.data[1:] {
		if uint32(v) == prev+1 {
			prev = uint32(v)
			continue
		}
		r.runs = append(r.runs, interval{uint16(start), uint16(prev)})
		start, prev = uint32(v), uint32(v)
	}
	r.runs = append(r.runs, interval{uint16(start), uint16(prev)})
	return r
}

func (a *arrayContainer) clone() container {
	d := make([]uint16, len(a.data))
	copy(d, a.data)
	return &arrayContainer{data: d}
}

func (a *arrayContainer) foreach(fn func(uint16) bool) {
	for _, v := range a.data {
		if !fn(v) {
			return
		}
	}
}

// ---------------------------------------------------------------------------
// bitmap container: fixed 8KB bitset
// ---------------------------------------------------------------------------

type bitmapContainer struct {
	containerBase
	words [bitmapWords]uint64
	card  int
}

func (b *bitmapContainer) typeID() containerType { return typeBitmap }
func (b *bitmapContainer) cardinality() int      { return b.card }

func (b *bitmapContainer) contains(v uint16) bool {
	return b.words[v>>6]&(uint64(1)<<(v&63)) != 0
}

func (b *bitmapContainer) add(v uint16) container {
	w := v >> 6
	mask := uint64(1) << (v & 63)
	if b.words[w]&mask == 0 {
		b.words[w] |= mask
		b.card++
	}
	if b.card == containerSize {
		return fullRunContainer()
	}
	return b
}

func rangeMask(wordIdx int, lo, hi uint16) uint64 {
	mask := ^uint64(0)
	if wordIdx == int(lo>>6) {
		mask &= ^uint64(0) << (lo & 63)
	}
	if wordIdx == int(hi>>6) {
		mask &= ^uint64(0) >> (63 - uint(hi&63))
	}
	return mask
}

func (b *bitmapContainer) addRange(lo, hi uint16) container {
	if lo > hi {
		return b
	}
	for w := int(lo >> 6); w <= int(hi>>6); w++ {
		mask := rangeMask(w, lo, hi)
		old := b.words[w]
		b.card += bits.OnesCount64(mask &^ old)
		b.words[w] = old | mask
	}
	if b.card == containerSize {
		return fullRunContainer()
	}
	return b
}

func (b *bitmapContainer) toArray() *arrayContainer {
	a := &arrayContainer{data: make([]uint16, 0, b.card)}
	b.foreach(func(v uint16) bool {
		a.data = append(a.data, v)
		return true
	})
	return a
}

func (b *bitmapContainer) toRun() *runContainer {
	r := &runContainer{card: b.card}
	start, prev := -1, -1
	b.foreach(func(v uint16) bool {
		vi := int(v)
		if start < 0 {
			start, prev = vi, vi
			return true
		}
		if vi == prev+1 {
			prev = vi
			return true
		}
		r.runs = append(r.runs, interval{uint16(start), uint16(prev)})
		start, prev = vi, vi
		return true
	})
	if start >= 0 {
		r.runs = append(r.runs, interval{uint16(start), uint16(prev)})
	}
	return r
}

func (b *bitmapContainer) clone() container {
	nb := &bitmapContainer{words: b.words, card: b.card}
	return nb
}

func (b *bitmapContainer) foreach(fn func(uint16) bool) {
	for w := 0; w < bitmapWords; w++ {
		word := b.words[w]
		for word != 0 {
			t := word & (-word)
			if !fn(uint16(w*64 + bits.TrailingZeros64(word))) {
				return
			}
			word ^= t
		}
	}
}

// ---------------------------------------------------------------------------
// run container: sorted list of non-overlapping, non-adjacent intervals
// ---------------------------------------------------------------------------

type interval struct {
	start, last uint16 // inclusive
}

type runContainer struct {
	containerBase
	runs []interval
	card int
}

func fullRunContainer() *runContainer {
	return &runContainer{runs: []interval{{0, 0xffff}}, card: containerSize}
}

func (r *runContainer) typeID() containerType { return typeRun }
func (r *runContainer) cardinality() int      { return r.card }

func cardOfRuns(runs []interval) int {
	n := 0
	for _, iv := range runs {
		n += int(iv.last) - int(iv.start) + 1
	}
	return n
}

// searchRuns returns the index of the last run with start <= v, or -1.
func searchRuns(runs []interval, v uint16) int {
	lo, hi := 0, len(runs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if runs[mid].start <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1
}

func (r *runContainer) contains(v uint16) bool {
	i := searchRuns(r.runs, v)
	return i >= 0 && v <= r.runs[i].last
}

func (r *runContainer) add(v uint16) container {
	n := len(r.runs)
	lo, hi := 0, n
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if uint32(r.runs[mid].start) <= uint32(v) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	i := lo
	if i > 0 && uint32(v) <= uint32(r.runs[i-1].last) {
		return r // already present
	}
	mergeLeft := i > 0 && uint32(r.runs[i-1].last)+1 == uint32(v)
	mergeRight := i < n && uint32(v)+1 == uint32(r.runs[i].start)
	switch {
	case mergeLeft && mergeRight:
		r.runs[i-1].last = r.runs[i].last
		r.runs = append(r.runs[:i], r.runs[i+1:]...)
	case mergeLeft:
		r.runs[i-1].last = v
	case mergeRight:
		r.runs[i].start = v
	default:
		r.runs = append(r.runs, interval{})
		copy(r.runs[i+1:], r.runs[i:])
		r.runs[i] = interval{v, v}
	}
	r.card++
	if len(r.runs) > runMaxCount {
		return r.toBitmap()
	}
	return r
}

func (r *runContainer) addRange(lo, hi uint16) container {
	if lo > hi {
		return r
	}
	lo32, hi32 := uint32(lo), uint32(hi)
	out := make([]interval, 0, len(r.runs)+1)
	merged := interval{lo, hi}
	placed := false
	for _, iv := range r.runs {
		switch {
		case uint32(iv.last)+1 < lo32:
			out = append(out, iv)
		case uint32(iv.start) > hi32+1:
			if !placed {
				out = append(out, merged)
				placed = true
			}
			out = append(out, iv)
		default: // overlap or adjacency: merge
			if iv.start < merged.start {
				merged.start = iv.start
			}
			if iv.last > merged.last {
				merged.last = iv.last
			}
		}
	}
	if !placed {
		out = append(out, merged)
	}
	r.runs = out
	r.card = cardOfRuns(out)
	if len(r.runs) > runMaxCount {
		return r.toBitmap()
	}
	return r
}

// flipRange toggles all bits in [lo, hi] (symmetric difference with the
// interval), used to implement XOR.
func (r *runContainer) flipRange(lo, hi uint16) {
	lo32, hi32 := uint32(lo), uint32(hi)
	out := make([]interval, 0, len(r.runs)+1)
	i := 0
	for i < len(r.runs) && uint32(r.runs[i].last) < lo32 {
		out = append(out, r.runs[i])
		i++
	}
	// A run straddling lo keeps its prefix below lo.
	if i < len(r.runs) && uint32(r.runs[i].start) < lo32 {
		out = append(out, interval{r.runs[i].start, uint16(lo32 - 1)})
	}
	pos := lo32
	for pos <= hi32 {
		if i < len(r.runs) && uint32(r.runs[i].start) <= pos {
			// pos is inside an existing run: excluded until the run ends
			if uint32(r.runs[i].last) >= hi32 {
				// A run extending beyond hi keeps its suffix above hi.
				if uint32(r.runs[i].last) > hi32 {
					out = append(out, interval{uint16(hi32 + 1), r.runs[i].last})
				}
				pos = hi32 + 1
			} else {
				pos = uint32(r.runs[i].last) + 1
			}
			i++
		} else {
			end := hi32
			if i < len(r.runs) && uint32(r.runs[i].start) > pos && uint32(r.runs[i].start)-1 < end {
				end = uint32(r.runs[i].start) - 1
			}
			out = append(out, interval{uint16(pos), uint16(end)})
			pos = end + 1
		}
	}
	for ; i < len(r.runs); i++ {
		out = append(out, r.runs[i])
	}
	r.runs = mergeAdjacent(out)
	r.card = cardOfRuns(r.runs)
}

func mergeAdjacent(runs []interval) []interval {
	if len(runs) == 0 {
		return runs
	}
	out := runs[:1]
	for _, iv := range runs[1:] {
		top := &out[len(out)-1]
		if uint32(iv.start) <= uint32(top.last)+1 {
			if iv.last > top.last {
				top.last = iv.last
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

func (r *runContainer) toArray() *arrayContainer {
	a := &arrayContainer{data: make([]uint16, 0, r.card)}
	for _, iv := range r.runs {
		for v := uint32(iv.start); v <= uint32(iv.last); v++ {
			a.data = append(a.data, uint16(v))
		}
	}
	return a
}

func (r *runContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{card: r.card}
	for _, iv := range r.runs {
		for w := int(iv.start >> 6); w <= int(iv.last>>6); w++ {
			b.words[w] |= rangeMask(w, iv.start, iv.last)
		}
	}
	return b
}

func (r *runContainer) clone() container {
	runs := make([]interval, len(r.runs))
	copy(runs, r.runs)
	return &runContainer{runs: runs, card: r.card}
}

func (r *runContainer) foreach(fn func(uint16) bool) {
	for _, iv := range r.runs {
		for v := uint32(iv.start); v <= uint32(iv.last); v++ {
			if !fn(uint16(v)) {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// normalization helpers: pick the most compact representation
// ---------------------------------------------------------------------------

func normalizeRun(r *runContainer) container {
	if r.card <= arrayMaxSize {
		return r.toArray()
	}
	if len(r.runs) >= runMaxCount {
		return r.toBitmap()
	}
	return r
}

func normalizeBitmap(b *bitmapContainer) container {
	if b.card <= arrayMaxSize {
		return b.toArray()
	}
	if b.card == containerSize {
		return fullRunContainer()
	}
	return b
}
