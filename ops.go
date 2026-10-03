package roaring

import (
	"math/bits"
	"sort"
)

// intersectContainers returns the intersection of two containers, choosing
// the most compact representation for the result.
func intersectContainers(a, b container) container {
	if ab, ok := a.(*bitmapContainer); ok {
		if bb, ok := b.(*bitmapContainer); ok {
			return andBitmap(ab, bb)
		}
	}
	if ar, ok := a.(*runContainer); ok {
		if br, ok := b.(*runContainer); ok {
			return andRun(ar, br)
		}
	}
	if aa, ok := a.(*arrayContainer); ok {
		if ba, ok := b.(*arrayContainer); ok {
			return andArray(aa, ba)
		}
	}
	// Generic path: iterate the smaller container, probe the larger.
	if a.cardinality() > b.cardinality() {
		a, b = b, a
	}
	var out []uint16
	a.foreach(func(x uint16) {
		if b.contains(x) {
			out = append(out, x)
		}
	})
	return convertFromSorted(out)
}

func andBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range out.words {
		w := a.words[i] & b.words[i]
		out.words[i] = w
		out.card += bits.OnesCount64(w)
	}
	return optimizeBitmap(out)
}

func andArray(a, b *arrayContainer) container {
	var out []uint16
	i, j := 0, 0
	for i < len(a.vals) && j < len(b.vals) {
		switch {
		case a.vals[i] < b.vals[j]:
			i++
		case a.vals[i] > b.vals[j]:
			j++
		default:
			out = append(out, a.vals[i])
			i++
			j++
		}
	}
	return convertFromSorted(out)
}

func andRun(a, b *runContainer) container {
	var runs []interval16
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		ra, rb := a.runs[i], b.runs[j]
		lo, hi := ra.start, ra.last
		if rb.start > lo {
			lo = rb.start
		}
		if rb.last < hi {
			hi = rb.last
		}
		if lo <= hi {
			runs = append(runs, interval16{start: lo, last: hi})
		}
		if ra.last < rb.last {
			i++
		} else {
			j++
		}
	}
	rc := &runContainer{runs: runs}
	card := rc.cardinality()
	if card == 0 {
		return &arrayContainer{}
	}
	if len(runs) >= runMaxCount {
		// Intersecting two near-limit run containers can produce more
		// runs than a run container may hold; re-select the encoding.
		return convertFromSorted(rc.toSortedSlice())
	}
	if card <= arrayMaxSize && len(runs)*2 >= card {
		return &arrayContainer{vals: rc.toSortedSlice()}
	}
	return rc
}

// unionContainers returns the union of two containers.
func unionContainers(a, b container) container {
	if ab, ok := a.(*bitmapContainer); ok {
		if bb, ok := b.(*bitmapContainer); ok {
			return orBitmap(ab, bb)
		}
	}
	if ar, ok := a.(*runContainer); ok {
		if br, ok := b.(*runContainer); ok {
			return unionRun(ar, br)
		}
	}
	if a.cardinality()+b.cardinality() > arrayMaxSize {
		bm := &bitmapContainer{}
		a.foreach(func(x uint16) { bm.add(x) })
		b.foreach(func(x uint16) { bm.add(x) })
		return optimizeBitmap(bm)
	}
	return convertFromSorted(mergeSorted(a.toSortedSlice(), b.toSortedSlice()))
}

func orBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range out.words {
		w := a.words[i] | b.words[i]
		out.words[i] = w
		out.card += bits.OnesCount64(w)
	}
	return optimizeBitmap(out)
}

func unionRun(a, b *runContainer) container {
	merged := make([]interval16, 0, len(a.runs)+len(b.runs))
	push := func(iv interval16) {
		if n := len(merged); n > 0 && uint32(iv.start) <= uint32(merged[n-1].last)+1 {
			if iv.last > merged[n-1].last {
				merged[n-1].last = iv.last
			}
			return
		}
		merged = append(merged, iv)
	}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		if a.runs[i].start < b.runs[j].start {
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
	rc := &runContainer{runs: merged}
	if len(merged) >= runMaxCount {
		return convertFromSorted(rc.toSortedSlice())
	}
	return rc
}

// xorContainers returns the symmetric difference of two containers.
func xorContainers(a, b container) container {
	if ab, ok := a.(*bitmapContainer); ok {
		if bb, ok := b.(*bitmapContainer); ok {
			return xorBitmap(ab, bb)
		}
	}
	if ar, ok := a.(*runContainer); ok {
		if br, ok := b.(*runContainer); ok {
			return xorRun(ar, br)
		}
	}
	return convertFromSorted(symDiffSorted(a.toSortedSlice(), b.toSortedSlice()))
}

// xorRun computes the symmetric difference of two run containers with a
// sweep over interval boundary events, avoiding materialization of every
// covered value (which can be up to 65536 per run).
func xorRun(a, b *runContainer) container {
	type event struct {
		pos   uint32
		delta int
	}
	events := make([]event, 0, 2*(len(a.runs)+len(b.runs)))
	for _, c := range []*runContainer{a, b} {
		for _, iv := range c.runs {
			events = append(events,
				event{pos: uint32(iv.start), delta: 1},
				event{pos: uint32(iv.last) + 1, delta: -1})
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].pos < events[j].pos })
	var runs []interval16
	active := 0
	runStart := -1
	for i := 0; i < len(events); {
		p := events[i].pos
		for i < len(events) && events[i].pos == p {
			active += events[i].delta
			i++
		}
		switch {
		case active == 1 && runStart < 0:
			runStart = int(p)
		case active != 1 && runStart >= 0:
			runs = append(runs, interval16{uint16(runStart), uint16(p - 1)})
			runStart = -1
		}
	}
	if runStart >= 0 {
		runs = append(runs, interval16{uint16(runStart), 65535})
	}
	rc := &runContainer{runs: runs}
	card := rc.cardinality()
	if card == 0 {
		return &arrayContainer{}
	}
	if len(runs) >= runMaxCount {
		return convertFromSorted(rc.toSortedSlice())
	}
	if card <= arrayMaxSize && len(runs)*2 >= card {
		return &arrayContainer{vals: rc.toSortedSlice()}
	}
	return rc
}

func xorBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range out.words {
		w := a.words[i] ^ b.words[i]
		out.words[i] = w
		out.card += bits.OnesCount64(w)
	}
	return optimizeBitmap(out)
}

func mergeSorted(a, b []uint16) []uint16 {
	out := make([]uint16, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

func symDiffSorted(a, b []uint16) []uint16 {
	var out []uint16
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			out = append(out, b[j])
			j++
		default:
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}
