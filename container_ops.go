package bitmap

import "math/bits"

// Binary container operations. All functions are pure: inputs are never
// mutated and results are freshly allocated and optimized.

func and(a, b container) container {
	switch ta := a.(type) {
	case *arrayContainer:
		switch tb := b.(type) {
		case *arrayContainer:
			return intersectArrays(ta, tb)
		case *bitmapContainer:
			return intersectArrayBitmap(ta, tb)
		case *runContainer:
			return intersectArrayRun(ta, tb)
		}
	case *bitmapContainer:
		switch tb := b.(type) {
		case *arrayContainer:
			return intersectArrayBitmap(tb, ta)
		case *bitmapContainer:
			return intersectBitmaps(ta, tb)
		case *runContainer:
			return intersectBitmapRun(ta, tb)
		}
	case *runContainer:
		switch tb := b.(type) {
		case *arrayContainer:
			return intersectArrayRun(tb, ta)
		case *bitmapContainer:
			return intersectBitmapRun(tb, ta)
		case *runContainer:
			return intersectRuns(ta, tb)
		}
	}
	return &arrayContainer{}
}

func or(a, b container) container {
	switch ta := a.(type) {
	case *arrayContainer:
		switch tb := b.(type) {
		case *arrayContainer:
			return unionArrays(ta, tb)
		case *bitmapContainer:
			return unionArrayBitmap(ta, tb)
		case *runContainer:
			return unionArrayRun(ta, tb)
		}
	case *bitmapContainer:
		switch tb := b.(type) {
		case *arrayContainer:
			return unionArrayBitmap(tb, ta)
		case *bitmapContainer:
			return unionBitmaps(ta, tb)
		case *runContainer:
			return unionBitmapRun(ta, tb)
		}
	case *runContainer:
		switch tb := b.(type) {
		case *arrayContainer:
			return unionArrayRun(tb, ta)
		case *bitmapContainer:
			return unionBitmapRun(tb, ta)
		case *runContainer:
			return unionRuns(ta, tb)
		}
	}
	return &arrayContainer{}
}

func xor(a, b container) container {
	switch ta := a.(type) {
	case *arrayContainer:
		switch tb := b.(type) {
		case *arrayContainer:
			return xorArrays(ta, tb)
		case *bitmapContainer:
			return xorArrayBitmap(ta, tb)
		case *runContainer:
			return xorArrayRun(ta, tb)
		}
	case *bitmapContainer:
		switch tb := b.(type) {
		case *arrayContainer:
			return xorArrayBitmap(tb, ta)
		case *bitmapContainer:
			return xorBitmaps(ta, tb)
		case *runContainer:
			return xorBitmapRun(ta, tb)
		}
	case *runContainer:
		switch tb := b.(type) {
		case *arrayContainer:
			return xorArrayRun(tb, ta)
		case *bitmapContainer:
			return xorBitmapRun(tb, ta)
		case *runContainer:
			return xorRuns(ta, tb)
		}
	}
	return &arrayContainer{}
}

// ---------------------------------------------------------------------------
// AND
// ---------------------------------------------------------------------------

func intersectArrays(a, b *arrayContainer) container {
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

func intersectArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, len(a.data))}
	for _, x := range a.data {
		if b.contains(x) {
			out.data = append(out.data, x)
		}
	}
	return out
}

func intersectArrayRun(a *arrayContainer, r *runContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, len(a.data))}
	for _, x := range a.data {
		if r.contains(x) {
			out.data = append(out.data, x)
		}
	}
	return out
}

func intersectBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.words[i] & b.words[i]
		out.words[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return optimizeBitmap(out)
}

func intersectBitmapRun(b *bitmapContainer, r *runContainer) container {
	return intersectBitmaps(b, r.toBitmap())
}

func intersectRuns(a, b *runContainer) container {
	out := &runContainer{}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		ra, rb := a.runs[i], b.runs[j]
		start := ra.start
		if rb.start > start {
			start = rb.start
		}
		last := ra.last
		if rb.last < last {
			last = rb.last
		}
		if start <= last {
			out.runs = append(out.runs, interval{start, last})
		}
		if ra.last < rb.last {
			i++
		} else {
			j++
		}
	}
	return optimizeRun(out)
}

// ---------------------------------------------------------------------------
// OR
// ---------------------------------------------------------------------------

func unionArrays(a, b *arrayContainer) container {
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
	if len(out) > arrayMaxSize {
		return optimizeBitmap((&arrayContainer{data: out}).toBitmap())
	}
	return &arrayContainer{data: out}
}

func unionArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, x := range a.data {
		mask := uint64(1) << (x & 63)
		w := &out.words[x>>6]
		if *w&mask == 0 {
			*w |= mask
			out.card++
		}
	}
	return optimizeBitmap(out)
}

func unionArrayRun(a *arrayContainer, r *runContainer) container {
	out := r.clone().(*runContainer)
	for _, x := range a.data {
		if !out.contains(x) {
			out.addRange(x, x)
		}
	}
	return optimizeRun(out)
}

func unionBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.words[i] | b.words[i]
		out.words[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return optimizeBitmap(out)
}

func unionBitmapRun(b *bitmapContainer, r *runContainer) container {
	return unionBitmaps(b, r.toBitmap())
}

func unionRuns(a, b *runContainer) container {
	out := &runContainer{}
	i, j := 0, 0
	var cur interval
	have := false
	push := func(iv interval) {
		if !have {
			cur = iv
			have = true
			return
		}
		if uint32(iv.start) <= uint32(cur.last)+1 {
			if iv.last > cur.last {
				cur.last = iv.last
			}
		} else {
			out.runs = append(out.runs, cur)
			cur = iv
		}
	}
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
	if have {
		out.runs = append(out.runs, cur)
	}
	return optimizeRun(out)
}

// ---------------------------------------------------------------------------
// XOR
// ---------------------------------------------------------------------------

func xorArrays(a, b *arrayContainer) container {
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
	if len(out) > arrayMaxSize {
		return optimizeBitmap((&arrayContainer{data: out}).toBitmap())
	}
	return &arrayContainer{data: out}
}

func xorArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, x := range a.data {
		mask := uint64(1) << (x & 63)
		w := &out.words[x>>6]
		if *w&mask == 0 {
			out.card++
		} else {
			out.card--
		}
		*w ^= mask
	}
	return optimizeBitmap(out)
}

func xorArrayRun(a *arrayContainer, r *runContainer) container {
	out := r.toBitmap()
	for _, x := range a.data {
		mask := uint64(1) << (x & 63)
		w := &out.words[x>>6]
		if *w&mask == 0 {
			out.card++
		} else {
			out.card--
		}
		*w ^= mask
	}
	return optimizeBitmap(out)
}

func xorBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.words[i] ^ b.words[i]
		out.words[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return optimizeBitmap(out)
}

func xorBitmapRun(b *bitmapContainer, r *runContainer) container {
	return xorBitmaps(b, r.toBitmap())
}

func xorRuns(a, b *runContainer) container {
	return xorBitmaps(a.toBitmap(), b.toBitmap())
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
