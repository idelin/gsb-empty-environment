package roaring

import (
	"math/bits"
	"sort"
)

// containerBuilder accumulates values into an array container, upgrading to
// a bitmap container when the threshold is exceeded.
type containerBuilder struct {
	arr []uint16
	bc  *bitmapContainer
}

func (b *containerBuilder) add(x uint16) {
	if b.bc != nil {
		b.bc.set(x)
		return
	}
	b.arr = append(b.arr, x)
	if len(b.arr) > arrayDefaultMaxSize {
		b.bc = newBitmapContainer()
		for _, v := range b.arr {
			b.bc.set(v)
		}
		b.arr = nil
	}
}

func (b *containerBuilder) result() container {
	if b.bc != nil {
		return b.bc
	}
	sort.Slice(b.arr, func(i, j int) bool { return b.arr[i] < b.arr[j] })
	return &arrayContainer{content: b.arr}
}

func intersect(a, b container) container {
	switch at := a.(type) {
	case *arrayContainer:
		switch bt := b.(type) {
		case *arrayContainer:
			return intersectArrayArray(at, bt)
		case *bitmapContainer:
			return intersectArrayBitmap(at, bt)
		}
	case *bitmapContainer:
		switch bt := b.(type) {
		case *arrayContainer:
			return intersectArrayBitmap(bt, at)
		case *bitmapContainer:
			return intersectBitmapBitmap(at, bt)
		}
	case *runContainer:
		if bt, ok := b.(*runContainer); ok {
			return intersectRunRun(at, bt)
		}
	}
	return intersectGeneric(a, b)
}

func intersectGeneric(a, b container) container {
	small, big := a, b
	if big.cardinality() < small.cardinality() {
		small, big = big, small
	}
	bld := &containerBuilder{}
	small.forEach(func(x uint16) {
		if big.contains(x) {
			bld.add(x)
		}
	})
	return bld.result()
}

func intersectArrayArray(a, b *arrayContainer) container {
	out := &arrayContainer{content: make([]uint16, 0, min(len(a.content), len(b.content)))}
	i, j := 0, 0
	for i < len(a.content) && j < len(b.content) {
		switch {
		case a.content[i] < b.content[j]:
			i++
		case a.content[i] > b.content[j]:
			j++
		default:
			out.content = append(out.content, a.content[i])
			i++
			j++
		}
	}
	return out
}

func intersectArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := &arrayContainer{content: make([]uint16, 0, len(a.content))}
	for _, v := range a.content {
		if b.contains(v) {
			out.content = append(out.content, v)
		}
	}
	return out
}

func intersectBitmapBitmap(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	card := 0
	for i := range out.bitmap {
		w := a.bitmap[i] & b.bitmap[i]
		out.bitmap[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return out
}

func intersectRunRun(a, b *runContainer) container {
	out := &runContainer{runs: make([]interval16, 0, min(len(a.runs), len(b.runs)))}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		aStart, aEnd := uint32(a.runs[i].start), runEnd(a.runs[i])
		bStart, bEnd := uint32(b.runs[j].start), runEnd(b.runs[j])
		start := max(aStart, bStart)
		end := min(aEnd, bEnd)
		if start <= end {
			out.runs = append(out.runs, interval16{start: uint16(start), length: uint16(end - start)})
		}
		if aEnd < bEnd {
			i++
		} else {
			j++
		}
	}
	return out
}

func union(a, b container) container {
	switch at := a.(type) {
	case *bitmapContainer:
		switch bt := b.(type) {
		case *bitmapContainer:
			return unionBitmapBitmap(at, bt)
		case *arrayContainer:
			return unionBitmapArray(at, bt)
		case *runContainer:
			return unionBitmapRun(at, bt)
		}
	case *arrayContainer:
		switch bt := b.(type) {
		case *bitmapContainer:
			return unionBitmapArray(bt, at)
		case *arrayContainer:
			return unionArrayArray(at, bt)
		case *runContainer:
			return unionArrayRun(at, bt)
		}
	case *runContainer:
		switch bt := b.(type) {
		case *bitmapContainer:
			return unionBitmapRun(bt, at)
		case *arrayContainer:
			return unionArrayRun(bt, at)
		case *runContainer:
			return unionRunRun(at, bt)
		}
	}
	return nil
}

func unionBitmapBitmap(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	card := 0
	for i := range out.bitmap {
		w := a.bitmap[i] | b.bitmap[i]
		out.bitmap[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return out
}

func unionBitmapArray(b *bitmapContainer, a *arrayContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, v := range a.content {
		out.set(v)
	}
	return out
}

func unionBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, iv := range r.runs {
		out.addRange(iv.start, uint16(runEnd(iv)))
	}
	return out
}

func unionArrayArray(a, b *arrayContainer) container {
	if len(a.content)+len(b.content) > arrayDefaultMaxSize {
		bc := newBitmapContainer()
		for _, v := range a.content {
			bc.set(v)
		}
		for _, v := range b.content {
			bc.set(v)
		}
		return bc
	}
	out := &arrayContainer{content: make([]uint16, 0, len(a.content)+len(b.content))}
	i, j := 0, 0
	for i < len(a.content) && j < len(b.content) {
		switch {
		case a.content[i] < b.content[j]:
			out.content = append(out.content, a.content[i])
			i++
		case a.content[i] > b.content[j]:
			out.content = append(out.content, b.content[j])
			j++
		default:
			out.content = append(out.content, a.content[i])
			i++
			j++
		}
	}
	out.content = append(out.content, a.content[i:]...)
	out.content = append(out.content, b.content[j:]...)
	return out
}

func unionArrayRun(a *arrayContainer, r *runContainer) container {
	// Per-value insertion into a run container is O(runs) each; for
	// larger arrays it is cheaper to go through a bitmap container.
	if len(a.content) > 512 {
		return unionBitmapRun(bitmapContainerFrom(a), r)
	}
	out := r.clone().(*runContainer)
	for _, v := range a.content {
		out.add(v)
	}
	return out
}

func unionRunRun(a, b *runContainer) container {
	out := &runContainer{runs: make([]interval16, 0, len(a.runs)+len(b.runs))}
	var cur interval16
	have := false
	push := func(s, e uint32) {
		if have && uint32(cur.start)+uint32(cur.length)+1 >= s {
			if e > runEnd(cur) {
				cur.length = uint16(e - uint32(cur.start))
			}
			return
		}
		if have {
			out.runs = append(out.runs, cur)
		}
		cur = interval16{start: uint16(s), length: uint16(e - s)}
		have = true
	}
	i, j := 0, 0
	for i < len(a.runs) || j < len(b.runs) {
		if j >= len(b.runs) || (i < len(a.runs) && a.runs[i].start <= b.runs[j].start) {
			push(uint32(a.runs[i].start), runEnd(a.runs[i]))
			i++
		} else {
			push(uint32(b.runs[j].start), runEnd(b.runs[j]))
			j++
		}
	}
	if have {
		out.runs = append(out.runs, cur)
	}
	return out
}

func diff(a, b container) container {
	switch at := a.(type) {
	case *bitmapContainer:
		switch bt := b.(type) {
		case *bitmapContainer:
			return diffBitmapBitmap(at, bt)
		case *arrayContainer:
			return diffBitmapArray(at, bt)
		case *runContainer:
			return diffBitmapRun(at, bt)
		}
	case *arrayContainer:
		switch bt := b.(type) {
		case *arrayContainer:
			return diffArrayArray(at, bt)
		}
	case *runContainer:
		if bt, ok := b.(*runContainer); ok {
			return diffRunRun(at, bt)
		}
	}
	return diffGeneric(a, b)
}

func diffGeneric(a, b container) container {
	bld := &containerBuilder{}
	a.forEach(func(x uint16) {
		if !b.contains(x) {
			bld.add(x)
		}
	})
	return bld.result()
}

func diffBitmapBitmap(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	card := 0
	for i := range out.bitmap {
		w := a.bitmap[i] &^ b.bitmap[i]
		out.bitmap[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return out
}

func diffBitmapArray(a *bitmapContainer, b *arrayContainer) container {
	out := a.clone().(*bitmapContainer)
	for _, v := range b.content {
		out.unset(v)
	}
	return out
}

func diffBitmapRun(a *bitmapContainer, b *runContainer) container {
	out := a.clone().(*bitmapContainer)
	for _, iv := range b.runs {
		out.removeRange(iv.start, uint16(runEnd(iv)))
	}
	return out
}

func diffArrayArray(a, b *arrayContainer) container {
	out := &arrayContainer{content: make([]uint16, 0, len(a.content))}
	i, j := 0, 0
	for i < len(a.content) {
		for j < len(b.content) && b.content[j] < a.content[i] {
			j++
		}
		if j < len(b.content) && b.content[j] == a.content[i] {
			i++
			continue
		}
		out.content = append(out.content, a.content[i])
		i++
	}
	return out
}

func diffRunRun(a, b *runContainer) container {
	out := &runContainer{runs: make([]interval16, 0, len(a.runs)+1)}
	j := 0
	for _, r := range a.runs {
		s := uint32(r.start)
		e := runEnd(r)
		for j < len(b.runs) && s <= e {
			bs := uint32(b.runs[j].start)
			be := runEnd(b.runs[j])
			if be < s {
				j++
				continue
			}
			if bs > e {
				break
			}
			if bs > s {
				out.runs = append(out.runs, interval16{start: uint16(s), length: uint16(bs - 1 - s)})
			}
			if be >= e {
				s = e + 1
			} else {
				s = be + 1
				j++
			}
		}
		if s <= e {
			out.runs = append(out.runs, interval16{start: uint16(s), length: uint16(e - s)})
		}
	}
	return out
}

func xorContainers(a, b container) container {
	// Word-level XOR whenever a bitmap container is involved and the
	// other side is dense enough to justify the conversion.
	_, aIsBitmap := a.(*bitmapContainer)
	_, bIsBitmap := b.(*bitmapContainer)
	if (aIsBitmap && bIsBitmap) ||
		(aIsBitmap && b.cardinality() > arrayDefaultMaxSize) ||
		(bIsBitmap && a.cardinality() > arrayDefaultMaxSize) {
		return xorBitmapBitmap(asBitmap(a), asBitmap(b))
	}
	if ar, ok := a.(*runContainer); ok {
		if br, ok := b.(*runContainer); ok {
			return xorRunRun(ar, br)
		}
	}
	bld := &containerBuilder{}
	a.forEach(func(x uint16) {
		if !b.contains(x) {
			bld.add(x)
		}
	})
	b.forEach(func(x uint16) {
		if !a.contains(x) {
			bld.add(x)
		}
	})
	return bld.result()
}

func asBitmap(c container) *bitmapContainer {
	if bc, ok := c.(*bitmapContainer); ok {
		return bc
	}
	return bitmapContainerFrom(c)
}

// xorRunRun computes the symmetric difference of two run containers in a
// single O(runs) sweep: interval boundaries toggle membership parity.
func xorRunRun(a, b *runContainer) container {
	out := &runContainer{runs: make([]interval16, 0, len(a.runs)+len(b.runs))}
	const none = ^uint32(0)
	i, j := 0, 0
	aIn, bIn := false, false
	inside := false
	var runStart uint32
	for {
		next := none
		if i < len(a.runs) {
			if aIn {
				next = min(next, runEnd(a.runs[i])+1)
			} else {
				next = min(next, uint32(a.runs[i].start))
			}
		}
		if j < len(b.runs) {
			if bIn {
				next = min(next, runEnd(b.runs[j])+1)
			} else {
				next = min(next, uint32(b.runs[j].start))
			}
		}
		if next == none {
			break
		}
		if i < len(a.runs) {
			if aIn && runEnd(a.runs[i])+1 == next {
				aIn = false
				i++
			} else if !aIn && uint32(a.runs[i].start) == next {
				aIn = true
			}
		}
		if j < len(b.runs) {
			if bIn && runEnd(b.runs[j])+1 == next {
				bIn = false
				j++
			} else if !bIn && uint32(b.runs[j].start) == next {
				bIn = true
			}
		}
		newInside := aIn != bIn
		if inside && !newInside {
			out.runs = append(out.runs, interval16{start: uint16(runStart), length: uint16(next - 1 - runStart)})
		}
		if newInside && !inside {
			runStart = next
		}
		inside = newInside
	}
	return out
}

func xorBitmapBitmap(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	card := 0
	for i := range out.bitmap {
		w := a.bitmap[i] ^ b.bitmap[i]
		out.bitmap[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return out
}
