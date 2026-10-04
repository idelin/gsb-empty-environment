package bitmap

import "math/bits"

// Container-level set algebra. Every public entry point dispatches on the
// concrete container pair and normalizes the result via optimize(), so the
// most compact representation is kept automatically.

func andContainers(a, b container) container {
	switch x := a.(type) {
	case *arrayContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return andArrayArray(x, y)
		case *bitmapContainer:
			return andArrayBitmap(x, y)
		case *runContainer:
			return andArrayRun(x, y)
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return andArrayBitmap(y, x)
		case *bitmapContainer:
			return andBitmapBitmap(x, y)
		case *runContainer:
			return andBitmapRun(x, y)
		}
	case *runContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return andArrayRun(y, x)
		case *bitmapContainer:
			return andBitmapRun(y, x)
		case *runContainer:
			return andRunRun(x, y)
		}
	}
	panic("bitmap: unknown container type")
}

func orContainers(a, b container) container {
	switch x := a.(type) {
	case *arrayContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return orArrayArray(x, y)
		case *bitmapContainer:
			return orArrayBitmap(x, y)
		case *runContainer:
			return orArrayRun(x, y)
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return orArrayBitmap(y, x)
		case *bitmapContainer:
			return orBitmapBitmap(x, y)
		case *runContainer:
			return orBitmapRun(x, y)
		}
	case *runContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return orArrayRun(y, x)
		case *bitmapContainer:
			return orBitmapRun(y, x)
		case *runContainer:
			return orRunRun(x, y)
		}
	}
	panic("bitmap: unknown container type")
}

func xorContainers(a, b container) container {
	switch x := a.(type) {
	case *arrayContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return xorArrayArray(x, y)
		case *bitmapContainer:
			return xorArrayBitmap(x, y)
		case *runContainer:
			return xorArrayRun(x, y)
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return xorArrayBitmap(y, x)
		case *bitmapContainer:
			return xorBitmapBitmap(x, y)
		case *runContainer:
			return xorBitmapRun(x, y)
		}
	case *runContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return xorArrayRun(y, x)
		case *bitmapContainer:
			return xorBitmapRun(y, x)
		case *runContainer:
			return xorRunRun(x, y)
		}
	}
	panic("bitmap: unknown container type")
}

// --- AND ---

func andArrayArray(a, b *arrayContainer) container {
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

func andArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := &arrayContainer{}
	for _, v := range a.data {
		if b.contains(v) {
			out.data = append(out.data, v)
		}
	}
	return out
}

func andArrayRun(a *arrayContainer, r *runContainer) container {
	out := &arrayContainer{}
	for _, v := range a.data {
		if r.contains(v) {
			out.data = append(out.data, v)
		}
	}
	return out
}

func andBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	card := 0
	for i := range a.words {
		w := a.words[i] & b.words[i]
		out.words[i] = w
		card += popcount(w)
	}
	out.card = card
	return optimize(out)
}

func andBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := &bitmapContainer{}
	for _, iv := range r.runs {
		wlo, whi := iv.start>>6, iv.last>>6
		for w := wlo; w <= whi; w++ {
			mask := ^uint64(0)
			if w == wlo {
				mask &= ^uint64(0) << (iv.start & 63)
			}
			if w == whi {
				mask &= ^uint64(0) >> (63 - (iv.last & 63))
			}
			out.words[w] = b.words[w] & mask
		}
	}
	out.recount()
	return optimize(out)
}

func andRunRun(a, b *runContainer) container {
	n := len(a.runs)
	if len(b.runs) < n {
		n = len(b.runs)
	}
	out := &runContainer{runs: make([]interval, 0, n)}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		ra, rb := a.runs[i], b.runs[j]
		lo := max16(ra.start, rb.start)
		hi := min16(ra.last, rb.last)
		if lo <= hi {
			out.runs = append(out.runs, interval{lo, hi})
		}
		if ra.last < rb.last {
			i++
		} else {
			j++
		}
	}
	return optimize(out)
}

// --- OR ---

func orArrayArray(a, b *arrayContainer) container {
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
		return optimize(out.toBitmap())
	}
	return out
}

func orArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, v := range a.data {
		m := uint64(1) << (v & 63)
		out.words[v>>6] |= m
	}
	out.recount()
	return optimize(out)
}

func orArrayRun(a *arrayContainer, r *runContainer) container {
	out := r.clone().(*runContainer)
	var c container = out
	for _, v := range a.data {
		c = c.add(v)
	}
	return optimize(c)
}

func orBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	card := 0
	for i := range a.words {
		w := a.words[i] | b.words[i]
		out.words[i] = w
		card += popcount(w)
	}
	out.card = card
	return optimize(out)
}

func orBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, iv := range r.runs {
		out.setRange(iv.start, iv.last)
	}
	out.recount()
	return optimize(out)
}

func orRunRun(a, b *runContainer) container {
	out := &runContainer{runs: make([]interval, 0, len(a.runs)+len(b.runs))}
	push := func(iv interval) {
		if n := len(out.runs); n > 0 && int(iv.start) <= int(out.runs[n-1].last)+1 {
			if iv.last > out.runs[n-1].last {
				out.runs[n-1].last = iv.last
			}
			return
		}
		out.runs = append(out.runs, iv)
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
	return optimize(out)
}

// --- XOR ---

func xorArrayArray(a, b *arrayContainer) container {
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
		return optimize(out.toBitmap())
	}
	return out
}

func xorArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, v := range a.data {
		out.words[v>>6] ^= 1 << (v & 63)
	}
	out.recount()
	return optimize(out)
}

func xorArrayRun(a *arrayContainer, r *runContainer) container {
	out := r.toBitmap()
	for _, v := range a.data {
		out.words[v>>6] ^= 1 << (v & 63)
	}
	out.recount()
	return optimize(out)
}

func xorBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	card := 0
	for i := range a.words {
		w := a.words[i] ^ b.words[i]
		out.words[i] = w
		card += popcount(w)
	}
	out.card = card
	return optimize(out)
}

func xorBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, iv := range r.runs {
		out.flipRange(iv.start, iv.last)
	}
	out.recount()
	return optimize(out)
}

// xorRunRun computes the symmetric difference of two interval sets by
// merging the two sorted event streams (start:+1, end+1:-1) and emitting
// positions where coverage is exactly 1. O(runs), no event allocation.
func xorRunRun(a, b *runContainer) container {
	out := &runContainer{}
	cover, open := 0, -1
	i, j := 0, 0   // run indices
	ai, bi := 0, 0 // 0: start event pending, 1: end event pending
	const inf = containerBits + 1
	for i < len(a.runs) || j < len(b.runs) {
		apos, bpos := inf, inf
		if i < len(a.runs) {
			if ai == 0 {
				apos = int(a.runs[i].start)
			} else {
				apos = int(a.runs[i].last) + 1
			}
		}
		if j < len(b.runs) {
			if bi == 0 {
				bpos = int(b.runs[j].start)
			} else {
				bpos = int(b.runs[j].last) + 1
			}
		}
		pos := apos
		if bpos < pos {
			pos = bpos
		}
		if cover == 1 && open >= 0 && pos > open {
			out.runs = append(out.runs, interval{uint16(open), uint16(pos - 1)})
		}
		if apos == pos {
			if ai == 0 {
				cover++
				ai = 1
			} else {
				cover--
				ai = 0
				i++
			}
		}
		if bpos == pos {
			if bi == 0 {
				cover++
				bi = 1
			} else {
				cover--
				bi = 0
				j++
			}
		}
		if cover == 1 {
			open = pos
		} else {
			open = -1
		}
	}
	return optimize(out)
}

func popcount(w uint64) int { return bits.OnesCount64(w) }

func max16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}

func min16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}
