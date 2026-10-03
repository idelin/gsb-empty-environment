package cbitmap

import "math/bits"

func minInt(a, b int) int {
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

func minU16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// AND
// ---------------------------------------------------------------------------

func and(a, b container) container {
	switch a := a.(type) {
	case *arrayContainer:
		switch b := b.(type) {
		case *arrayContainer:
			return intersectArrays(a.data, b.data)
		case *bitmapContainer:
			return arrayAndBitmap(a, b)
		case *runContainer:
			return arrayAndRun(a, b)
		}
	case *bitmapContainer:
		switch b := b.(type) {
		case *arrayContainer:
			return arrayAndBitmap(b, a)
		case *bitmapContainer:
			return bitmapAndBitmap(a, b)
		case *runContainer:
			return bitmapAndRun(a, b)
		}
	case *runContainer:
		switch b := b.(type) {
		case *arrayContainer:
			return arrayAndRun(b, a)
		case *bitmapContainer:
			return bitmapAndRun(b, a)
		case *runContainer:
			return runAndRun(a, b)
		}
	}
	panic("cbitmap: unknown container type")
}

func intersectArrays(a, b []uint16) *arrayContainer {
	out := make([]uint16, 0, minInt(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return &arrayContainer{data: out}
}

func arrayAndBitmap(ac *arrayContainer, bc *bitmapContainer) *arrayContainer {
	out := make([]uint16, 0, len(ac.data))
	for _, v := range ac.data {
		if bc.words[v>>6]&(1<<(v&63)) != 0 {
			out = append(out, v)
		}
	}
	return &arrayContainer{data: out}
}

func arrayAndRun(ac *arrayContainer, rc *runContainer) *arrayContainer {
	out := make([]uint16, 0, len(ac.data))
	ri := 0
	for _, v := range ac.data {
		for ri < len(rc.runs) && rc.runs[ri].last() < v {
			ri++
		}
		if ri >= len(rc.runs) {
			break
		}
		if rc.runs[ri].start <= v {
			out = append(out, v)
		}
	}
	return &arrayContainer{data: out}
}

func bitmapAndBitmap(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	c := 0
	runs := 0
	prev := uint64(0)
	for i := range out.words {
		w := a.words[i] & b.words[i]
		out.words[i] = w
		c += bits.OnesCount64(w)
		runs += bits.OnesCount64(w &^ ((w << 1) | (prev >> 63)))
		prev = w
	}
	out.card = c
	return chooseBitmapEncoding(out, c, runs)
}

func bitmapAndRun(bc *bitmapContainer, rc *runContainer) container {
	out := newBitmapContainer()
	for _, r := range rc.runs {
		w0, w1, m0, m1 := rangeMask(r.start, r.last())
		if w0 == w1 {
			out.words[w0] |= bc.words[w0] & m0 & m1
			continue
		}
		out.words[w0] |= bc.words[w0] & m0
		for w := w0 + 1; w < w1; w++ {
			out.words[w] = bc.words[w]
		}
		out.words[w1] |= bc.words[w1] & m1
	}
	out.card = -1
	return out.optimize()
}

func runAndRun(a, b *runContainer) container {
	out := make([]run16, 0, minInt(len(a.runs), len(b.runs)))
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		ra, rb := a.runs[i], b.runs[j]
		lo := maxU16(ra.start, rb.start)
		hi := minU16(ra.last(), rb.last())
		if lo <= hi {
			out = append(out, run16{start: lo, length: hi - lo})
		}
		if ra.last() < rb.last() {
			i++
		} else {
			j++
		}
	}
	rc := &runContainer{runs: out, card: -1}
	return rc.optimize()
}

// ---------------------------------------------------------------------------
// OR
// ---------------------------------------------------------------------------

func or(a, b container) container {
	switch a := a.(type) {
	case *arrayContainer:
		switch b := b.(type) {
		case *arrayContainer:
			return unionArrays(a.data, b.data)
		case *bitmapContainer:
			return arrayOrBitmap(a, b)
		case *runContainer:
			return arrayOrBitmap(a, b.toBitmap())
		}
	case *bitmapContainer:
		switch b := b.(type) {
		case *arrayContainer:
			return arrayOrBitmap(b, a)
		case *bitmapContainer:
			return bitmapOrBitmap(a, b)
		case *runContainer:
			return bitmapOrRun(a, b)
		}
	case *runContainer:
		switch b := b.(type) {
		case *arrayContainer:
			return arrayOrBitmap(b, a.toBitmap())
		case *bitmapContainer:
			return bitmapOrRun(b, a)
		case *runContainer:
			return runOrRun(a, b)
		}
	}
	panic("cbitmap: unknown container type")
}

func unionArrays(a, b []uint16) container {
	out := make([]uint16, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		default:
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return (&arrayContainer{data: out}).optimize()
}

func arrayOrBitmap(ac *arrayContainer, bc *bitmapContainer) container {
	out := newBitmapContainer()
	copy(out.words, bc.words)
	for _, v := range ac.data {
		out.words[v>>6] |= 1 << (v & 63)
	}
	out.card = -1
	return out.optimize()
}

func bitmapOrBitmap(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	c := 0
	runs := 0
	prev := uint64(0)
	for i := range out.words {
		w := a.words[i] | b.words[i]
		out.words[i] = w
		c += bits.OnesCount64(w)
		runs += bits.OnesCount64(w &^ ((w << 1) | (prev >> 63)))
		prev = w
	}
	out.card = c
	return chooseBitmapEncoding(out, c, runs)
}

func bitmapOrRun(bc *bitmapContainer, rc *runContainer) container {
	out := newBitmapContainer()
	copy(out.words, bc.words)
	for _, r := range rc.runs {
		setRangeBits(out.words, r.start, r.last())
	}
	out.card = -1
	return out.optimize()
}

func runOrRun(a, b *runContainer) container {
	out := make([]run16, 0, len(a.runs)+len(b.runs))
	var cur run16
	have := false
	push := func(r run16) {
		if !have {
			cur = r
			have = true
			return
		}
		if uint32(r.start) <= uint32(cur.last())+1 {
			if r.last() > cur.last() {
				cur.length = r.last() - cur.start
			}
			return
		}
		out = append(out, cur)
		cur = r
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
	if have {
		out = append(out, cur)
	}
	rc := &runContainer{runs: out, card: -1}
	return rc.optimize()
}

// ---------------------------------------------------------------------------
// XOR
// ---------------------------------------------------------------------------

func xor(a, b container) container {
	switch a := a.(type) {
	case *arrayContainer:
		switch b := b.(type) {
		case *arrayContainer:
			return xorArrays(a.data, b.data)
		case *bitmapContainer:
			return arrayXorBitmap(a, b)
		case *runContainer:
			return arrayXorBitmap(a, b.toBitmap())
		}
	case *bitmapContainer:
		switch b := b.(type) {
		case *arrayContainer:
			return arrayXorBitmap(b, a)
		case *bitmapContainer:
			return bitmapXorBitmap(a, b)
		case *runContainer:
			return bitmapXorRun(a, b)
		}
	case *runContainer:
		switch b := b.(type) {
		case *arrayContainer:
			return arrayXorBitmap(b, a.toBitmap())
		case *bitmapContainer:
			return bitmapXorRun(b, a)
		case *runContainer:
			return runXorRun(a, b)
		}
	}
	panic("cbitmap: unknown container type")
}

func xorArrays(a, b []uint16) container {
	out := make([]uint16, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		default:
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return (&arrayContainer{data: out}).optimize()
}

func arrayXorBitmap(ac *arrayContainer, bc *bitmapContainer) container {
	out := newBitmapContainer()
	copy(out.words, bc.words)
	for _, v := range ac.data {
		out.words[v>>6] ^= 1 << (v & 63)
	}
	out.card = -1
	return out.optimize()
}

func bitmapXorBitmap(a, b *bitmapContainer) container {
	out := newBitmapContainer()
	c := 0
	runs := 0
	prev := uint64(0)
	for i := range out.words {
		w := a.words[i] ^ b.words[i]
		out.words[i] = w
		c += bits.OnesCount64(w)
		runs += bits.OnesCount64(w &^ ((w << 1) | (prev >> 63)))
		prev = w
	}
	out.card = c
	return chooseBitmapEncoding(out, c, runs)
}

func bitmapXorRun(bc *bitmapContainer, rc *runContainer) container {
	out := newBitmapContainer()
	copy(out.words, bc.words)
	for _, r := range rc.runs {
		flipRangeBits(out.words, r.start, r.last())
	}
	out.card = -1
	return out.optimize()
}

func runXorRun(a, b *runContainer) container {
	bc := a.toBitmap()
	for _, r := range b.runs {
		flipRangeBits(bc.words, r.start, r.last())
	}
	bc.card = -1
	return bc.optimize()
}
