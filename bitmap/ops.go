package bitmap

import (
	"math/bits"
	"sort"
)

func minU16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

func maxU16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}

func andContainers(a, b container) container {
	switch x := a.(type) {
	case *arrayContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return intersectArrays(x, y)
		case *bitmapContainer:
			return intersectArrayBitmap(x, y)
		case *runContainer:
			return intersectArrayRun(x, y)
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return intersectArrayBitmap(y, x)
		case *bitmapContainer:
			return intersectBitmaps(x, y)
		case *runContainer:
			return intersectBitmapRun(x, y)
		}
	case *runContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return intersectArrayRun(y, x)
		case *bitmapContainer:
			return intersectBitmapRun(y, x)
		case *runContainer:
			return intersectRuns(x, y)
		}
	}
	panic("bitmap: unknown container combination in AND")
}

func orContainers(a, b container) container {
	switch x := a.(type) {
	case *arrayContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return unionArrays(x, y)
		case *bitmapContainer:
			return unionArrayBitmap(x, y)
		case *runContainer:
			return unionArrayRun(x, y)
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return unionArrayBitmap(y, x)
		case *bitmapContainer:
			return unionBitmaps(x, y)
		case *runContainer:
			return unionBitmapRun(x, y)
		}
	case *runContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return unionArrayRun(y, x)
		case *bitmapContainer:
			return unionBitmapRun(y, x)
		case *runContainer:
			return unionRuns(x, y)
		}
	}
	panic("bitmap: unknown container combination in OR")
}

func xorContainers(a, b container) container {
	switch x := a.(type) {
	case *arrayContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return xorArrays(x, y)
		case *bitmapContainer:
			return xorArrayBitmap(x, y)
		case *runContainer:
			return xorArrayBitmap(x, y.toBitmap())
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return xorArrayBitmap(y, x)
		case *bitmapContainer:
			return xorBitmapContainers(x, y)
		case *runContainer:
			return xorBitmapRun(x, y)
		}
	case *runContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return xorArrayBitmap(y, x.toBitmap())
		case *bitmapContainer:
			return xorBitmapRun(y, x)
		case *runContainer:
			return xorRuns(x, y)
		}
	}
	panic("bitmap: unknown container combination in XOR")
}

// andCardContainers computes |a AND b| without materializing the result.
func andCardContainers(a, b container) int {
	if x, ok := a.(*bitmapContainer); ok {
		if y, ok := b.(*bitmapContainer); ok {
			c := 0
			for i := 0; i < bitmapWords; i++ {
				c += bits.OnesCount64(x.bits[i] & y.bits[i])
			}
			return c
		}
	}
	if x, ok := a.(*runContainer); ok {
		if y, ok := b.(*runContainer); ok {
			c := 0
			i, j := 0, 0
			for i < len(x.runs) && j < len(y.runs) {
				lo := maxU16(x.runs[i].start, y.runs[j].start)
				hi := minU16(x.runs[i].last, y.runs[j].last)
				if lo <= hi {
					c += int(hi) - int(lo) + 1
				}
				if x.runs[i].last < y.runs[j].last {
					i++
				} else {
					j++
				}
			}
			return c
		}
	}
	if x, ok := a.(*bitmapContainer); ok {
		if y, ok := b.(*runContainer); ok {
			return andCardBitmapRun(x, y)
		}
	}
	if x, ok := b.(*bitmapContainer); ok {
		if y, ok := a.(*runContainer); ok {
			return andCardBitmapRun(x, y)
		}
	}
	if a.cardinality() > b.cardinality() {
		a, b = b, a
	}
	c := 0
	a.foreach(func(x uint16) bool {
		if b.contains(x) {
			c++
		}
		return true
	})
	return c
}

func andCardBitmapRun(bc *bitmapContainer, rc *runContainer) int {
	c := 0
	for _, iv := range rc.runs {
		first, last := iv.start>>6, iv.last>>6
		for w := first; w <= last; w++ {
			mask := ^uint64(0)
			if w == first {
				mask &= ^uint64(0) << (iv.start & 63)
			}
			if w == last {
				mask &= ^uint64(0) >> (63 - (iv.last & 63))
			}
			c += bits.OnesCount64(bc.bits[w] & mask)
		}
	}
	return c
}

func intersectArrays(a, b *arrayContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, min(len(a.data), len(b.data)))}
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

func intersectArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := &arrayContainer{}
	for _, v := range a.data {
		if b.contains(v) {
			out.data = append(out.data, v)
		}
	}
	return out
}

func intersectArrayRun(a *arrayContainer, r *runContainer) container {
	out := &arrayContainer{}
	for _, v := range a.data {
		if r.contains(v) {
			out.data = append(out.data, v)
		}
	}
	return out
}

func intersectBitmaps(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	c := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.bits[i] & b.bits[i]
		out.bits[i] = w
		c += bits.OnesCount64(w)
	}
	out.card = c
	return out
}

func intersectBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := newBitmapContainer()
	c := 0
	for _, iv := range r.runs {
		first, last := iv.start>>6, iv.last>>6
		for w := first; w <= last; w++ {
			mask := ^uint64(0)
			if w == first {
				mask &= ^uint64(0) << (iv.start & 63)
			}
			if w == last {
				mask &= ^uint64(0) >> (63 - (iv.last & 63))
			}
			v := b.bits[w] & mask
			out.bits[w] = v
			c += bits.OnesCount64(v)
		}
	}
	out.card = c
	return out
}

func intersectRuns(a, b *runContainer) container {
	out := &runContainer{}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		lo := maxU16(a.runs[i].start, b.runs[j].start)
		hi := minU16(a.runs[i].last, b.runs[j].last)
		if lo <= hi {
			out.runs = append(out.runs, interval16{lo, hi})
		}
		if a.runs[i].last < b.runs[j].last {
			i++
		} else {
			j++
		}
	}
	return out
}

func unionArrays(a, b *arrayContainer) container {
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

func unionArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, v := range a.data {
		out.set(v)
	}
	return out
}

func unionArrayRun(a *arrayContainer, r *runContainer) container {
	var res container = r.clone()
	for _, v := range a.data {
		res, _ = res.add(v)
	}
	return res
}

func unionBitmaps(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	c := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.bits[i] | b.bits[i]
		out.bits[i] = w
		c += bits.OnesCount64(w)
	}
	out.card = c
	return out
}

func unionBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, iv := range r.runs {
		out.addRange(iv.start, iv.last)
	}
	return out
}

func unionRuns(a, b *runContainer) container {
	out := &runContainer{runs: make([]interval16, 0, len(a.runs)+len(b.runs))}
	var cur interval16
	started := false
	push := func(iv interval16) {
		if !started {
			cur = iv
			started = true
			return
		}
		if uint32(iv.start) <= uint32(cur.last)+1 {
			if iv.last > cur.last {
				cur.last = iv.last
			}
			return
		}
		out.runs = append(out.runs, cur)
		cur = iv
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
	if started {
		out.runs = append(out.runs, cur)
	}
	if len(out.runs)*runLengthOverhead > bitmapContainerBytes {
		return out.toBitmap()
	}
	return out
}

func xorArrays(a, b *arrayContainer) container {
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

func xorArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, v := range a.data {
		if out.contains(v) {
			out.unset(v)
		} else {
			out.set(v)
		}
	}
	return out
}

func xorBitmapContainers(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	c := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.bits[i] ^ b.bits[i]
		out.bits[i] = w
		c += bits.OnesCount64(w)
	}
	out.card = c
	return out
}

func xorBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, iv := range r.runs {
		out.flipRange(iv.start, iv.last)
	}
	return out
}

type runEvent struct {
	pos   uint32
	delta int
}

// xorRuns computes the symmetric difference of two run containers with a
// sweep over interval boundary events.
func xorRuns(a, b *runContainer) container {
	events := make([]runEvent, 0, 2*(len(a.runs)+len(b.runs)))
	for _, r := range a.runs {
		events = append(events, runEvent{uint32(r.start), 1}, runEvent{uint32(r.last) + 1, -1})
	}
	for _, r := range b.runs {
		events = append(events, runEvent{uint32(r.start), 1}, runEvent{uint32(r.last) + 1, -1})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].pos < events[j].pos })
	out := &runContainer{}
	count := 0
	inRun := false
	var open uint32
	for i := 0; i < len(events); {
		j := i
		net := 0
		for j < len(events) && events[j].pos == events[i].pos {
			net += events[j].delta
			j++
		}
		pos := events[i].pos
		count += net
		if !inRun && count == 1 {
			open = pos
			inRun = true
		} else if inRun && count != 1 {
			out.runs = append(out.runs, interval16{uint16(open), uint16(pos - 1)})
			inRun = false
		}
		i = j
	}
	if len(out.runs)*runLengthOverhead > bitmapContainerBytes {
		return out.toBitmap()
	}
	return out
}
