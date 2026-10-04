package roaring

import (
	"math/bits"
	"sort"
)

func maxu16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}

func minu16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

// containerAnd returns the intersection of two containers, normalized to
// the most compact representation. Inputs are treated as immutable.
func containerAnd(a, b container) container {
	if ra, ok := a.(*runContainer); ok {
		if rb, ok := b.(*runContainer); ok {
			return normalize(andRunRun(ra, rb))
		}
		return containerAnd(nonRun(ra), b)
	}
	if _, ok := b.(*runContainer); ok {
		return containerAnd(a, nonRun(b))
	}
	switch x := a.(type) {
	case *arrayContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return normalize(andArrayArray(x, y))
		case *bitmapContainer:
			return normalize(andArrayBitmap(x, y))
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return normalize(andArrayBitmap(y, x))
		case *bitmapContainer:
			return normalize(andBitmapBitmap(x, y))
		}
	}
	return nil
}

// containerOr returns the union of two containers, normalized.
func containerOr(a, b container) container {
	if ra, ok := a.(*runContainer); ok {
		if rb, ok := b.(*runContainer); ok {
			return normalize(orRunRun(ra, rb))
		}
		return containerOr(nonRun(ra), b)
	}
	if _, ok := b.(*runContainer); ok {
		return containerOr(a, nonRun(b))
	}
	switch x := a.(type) {
	case *arrayContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return normalize(orArrayArray(x, y))
		case *bitmapContainer:
			return normalize(orArrayBitmap(x, y))
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return normalize(orArrayBitmap(y, x))
		case *bitmapContainer:
			return normalize(orBitmapBitmap(x, y))
		}
	}
	return nil
}

// containerXor returns the symmetric difference of two containers, normalized.
func containerXor(a, b container) container {
	if ra, ok := a.(*runContainer); ok {
		if rb, ok := b.(*runContainer); ok {
			return normalize(xorRunRun(ra, rb))
		}
		return containerXor(nonRun(ra), b)
	}
	if _, ok := b.(*runContainer); ok {
		return containerXor(a, nonRun(b))
	}
	switch x := a.(type) {
	case *arrayContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return normalize(xorArrayArray(x, y))
		case *bitmapContainer:
			return normalize(xorArrayBitmap(x, y))
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return normalize(xorArrayBitmap(y, x))
		case *bitmapContainer:
			return normalize(xorBitmapBitmap(x, y))
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// AND kernels
// ---------------------------------------------------------------------------

func andArrayArray(a, b *arrayContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, minInt(len(a.data), len(b.data)))}
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

func andArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, len(a.data))}
	for _, v := range a.data {
		if b.contains(v) {
			out.data = append(out.data, v)
		}
	}
	return out
}

func andBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range a.bits {
		w := a.bits[i] & b.bits[i]
		out.bits[i] = w
		out.card += bits.OnesCount64(w)
	}
	return out
}

func andRunRun(a, b *runContainer) container {
	out := &runContainer{}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		ra, rb := a.runs[i], b.runs[j]
		lo := maxu16(ra.start, rb.start)
		hi := minu16(ra.last, rb.last)
		if lo <= hi {
			out.runs = append(out.runs, interval{lo, hi})
		}
		if ra.last < rb.last {
			i++
		} else {
			j++
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// OR kernels
// ---------------------------------------------------------------------------

func orArrayArray(a, b *arrayContainer) container {
	out := make([]uint16, 0, len(a.data)+len(b.data))
	i, j := 0, 0
	for i < len(a.data) && j < len(b.data) {
		switch {
		case a.data[i] < b.data[j]:
			out = append(out, a.data[i])
			i++
		case a.data[i] > b.data[j]:
			out = append(out, b.data[j])
			j++
		default:
			out = append(out, a.data[i])
			i++
			j++
		}
	}
	out = append(out, a.data[i:]...)
	out = append(out, b.data[j:]...)
	if len(out) > arrayMaxCardinality {
		bc := &bitmapContainer{card: len(out)}
		for _, v := range out {
			bc.bits[v>>6] |= uint64(1) << (v & 63)
		}
		return bc
	}
	return &arrayContainer{data: out}
}

func orArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, v := range a.data {
		mask := uint64(1) << (v & 63)
		w := &out.bits[v>>6]
		if *w&mask == 0 {
			*w |= mask
			out.card++
		}
	}
	return out
}

func orBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range a.bits {
		w := a.bits[i] | b.bits[i]
		out.bits[i] = w
		out.card += bits.OnesCount64(w)
	}
	return out
}

func orRunRun(a, b *runContainer) container {
	out := &runContainer{runs: make([]interval, 0, len(a.runs)+len(b.runs))}
	var cur interval
	open := false
	emit := func(iv interval) {
		if !open {
			cur = iv
			open = true
			return
		}
		if int(iv.start) <= int(cur.last)+1 {
			if iv.last > cur.last {
				cur.last = iv.last
			}
		} else {
			out.runs = append(out.runs, cur)
			cur = iv
		}
	}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		if a.runs[i].start <= b.runs[j].start {
			emit(a.runs[i])
			i++
		} else {
			emit(b.runs[j])
			j++
		}
	}
	for ; i < len(a.runs); i++ {
		emit(a.runs[i])
	}
	for ; j < len(b.runs); j++ {
		emit(b.runs[j])
	}
	if open {
		out.runs = append(out.runs, cur)
	}
	return out
}

// ---------------------------------------------------------------------------
// XOR kernels
// ---------------------------------------------------------------------------

func xorArrayArray(a, b *arrayContainer) container {
	out := make([]uint16, 0, len(a.data)+len(b.data))
	i, j := 0, 0
	for i < len(a.data) && j < len(b.data) {
		switch {
		case a.data[i] < b.data[j]:
			out = append(out, a.data[i])
			i++
		case a.data[i] > b.data[j]:
			out = append(out, b.data[j])
			j++
		default:
			i++
			j++
		}
	}
	out = append(out, a.data[i:]...)
	out = append(out, b.data[j:]...)
	if len(out) > arrayMaxCardinality {
		bc := &bitmapContainer{card: len(out)}
		for _, v := range out {
			bc.bits[v>>6] |= uint64(1) << (v & 63)
		}
		return bc
	}
	return &arrayContainer{data: out}
}

func xorArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, v := range a.data {
		mask := uint64(1) << (v & 63)
		w := &out.bits[v>>6]
		if *w&mask != 0 {
			*w &^= mask
			out.card--
		} else {
			*w |= mask
			out.card++
		}
	}
	return out
}

func xorBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range a.bits {
		w := a.bits[i] ^ b.bits[i]
		out.bits[i] = w
		out.card += bits.OnesCount64(w)
	}
	return out
}

// xorRunRun computes the symmetric difference of two interval sets with a
// parity sweep over sorted interval-boundary events.
func xorRunRun(a, b *runContainer) container {
	events := make([][2]int, 0, 2*(len(a.runs)+len(b.runs)))
	for _, r := range a.runs {
		events = append(events, [2]int{int(r.start), 1}, [2]int{int(r.last) + 1, -1})
	}
	for _, r := range b.runs {
		events = append(events, [2]int{int(r.start), 1}, [2]int{int(r.last) + 1, -1})
	}
	sort.Slice(events, func(i, j int) bool { return events[i][0] < events[j][0] })
	out := &runContainer{}
	count := 0
	open := false
	start := 0
	for i := 0; i < len(events); {
		pos := events[i][0]
		if open && pos > start {
			out.runs = append(out.runs, interval{uint16(start), uint16(pos - 1)})
		}
		for i < len(events) && events[i][0] == pos {
			count += events[i][1]
			i++
		}
		open = count%2 == 1
		start = pos
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
