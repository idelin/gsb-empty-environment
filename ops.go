package cbitmap

import (
	"math/bits"
	"sort"
)

// containerAnd computes the intersection of two containers of any kind.
func containerAnd(a, b container) container {
	switch ac := a.(type) {
	case *arrayContainer:
		switch bc := b.(type) {
		case *arrayContainer:
			return intersectArrays(ac, bc)
		case *bitmapContainer:
			return intersectArrayBitmap(ac, bc)
		case *runContainer:
			return intersectArrayRun(ac, bc)
		}
	case *bitmapContainer:
		switch bc := b.(type) {
		case *arrayContainer:
			return intersectArrayBitmap(bc, ac)
		case *bitmapContainer:
			return intersectBitmaps(ac, bc)
		case *runContainer:
			return intersectBitmapRun(ac, bc)
		}
	case *runContainer:
		switch bc := b.(type) {
		case *arrayContainer:
			return intersectArrayRun(bc, ac)
		case *bitmapContainer:
			return intersectBitmapRun(bc, ac)
		case *runContainer:
			return intersectRuns(ac, bc)
		}
	}
	panic("cbitmap: unknown container type")
}

// containerOr computes the union of two containers of any kind.
func containerOr(a, b container) container {
	switch ac := a.(type) {
	case *arrayContainer:
		switch bc := b.(type) {
		case *arrayContainer:
			return unionArrays(ac, bc)
		case *bitmapContainer:
			return unionArrayBitmap(ac, bc)
		case *runContainer:
			return unionArrayRun(ac, bc)
		}
	case *bitmapContainer:
		switch bc := b.(type) {
		case *arrayContainer:
			return unionArrayBitmap(bc, ac)
		case *bitmapContainer:
			return unionBitmaps(ac, bc)
		case *runContainer:
			return unionBitmapRun(ac, bc)
		}
	case *runContainer:
		switch bc := b.(type) {
		case *arrayContainer:
			return unionArrayRun(bc, ac)
		case *bitmapContainer:
			return unionBitmapRun(bc, ac)
		case *runContainer:
			return unionRuns(ac, bc)
		}
	}
	panic("cbitmap: unknown container type")
}

// containerXor computes the symmetric difference of two containers.
func containerXor(a, b container) container {
	switch ac := a.(type) {
	case *arrayContainer:
		switch bc := b.(type) {
		case *arrayContainer:
			return xorArrays(ac, bc)
		case *bitmapContainer:
			return xorArrayBitmap(ac, bc)
		case *runContainer:
			return xorArrayRun(ac, bc)
		}
	case *bitmapContainer:
		switch bc := b.(type) {
		case *arrayContainer:
			return xorArrayBitmap(bc, ac)
		case *bitmapContainer:
			return xorBitmaps(ac, bc)
		case *runContainer:
			return xorBitmapRun(ac, bc)
		}
	case *runContainer:
		switch bc := b.(type) {
		case *arrayContainer:
			return xorArrayRun(bc, ac)
		case *bitmapContainer:
			return xorBitmapRun(bc, ac)
		case *runContainer:
			return xorRuns(ac, bc)
		}
	}
	panic("cbitmap: unknown container type")
}

// ---------------------------------------------------------------------------
// AND helpers
// ---------------------------------------------------------------------------

func intersectArrays(a, b *arrayContainer) container {
	res := make([]uint16, 0, minInt(len(a.data), len(b.data)))
	i, j := 0, 0
	for i < len(a.data) && j < len(b.data) {
		switch {
		case a.data[i] < b.data[j]:
			i++
		case a.data[i] > b.data[j]:
			j++
		default:
			res = append(res, a.data[i])
			i++
			j++
		}
	}
	return &arrayContainer{data: res}
}

func intersectArrayBitmap(ac *arrayContainer, bc *bitmapContainer) container {
	res := make([]uint16, 0, len(ac.data))
	for _, v := range ac.data {
		if bc.contains(v) {
			res = append(res, v)
		}
	}
	return &arrayContainer{data: res}
}

func intersectArrayRun(ac *arrayContainer, rc *runContainer) container {
	res := make([]uint16, 0, len(ac.data))
	for _, v := range ac.data {
		if rc.contains(v) {
			res = append(res, v)
		}
	}
	return &arrayContainer{data: res}
}

func intersectBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range out.words {
		w := a.words[i] & b.words[i]
		out.words[i] = w
		out.card += bits.OnesCount64(w)
	}
	if out.card <= arrayMaxSize {
		return out.toArray()
	}
	return out
}

func intersectBitmapRun(bc *bitmapContainer, rc *runContainer) container {
	out := &bitmapContainer{}
	for _, iv := range rc.runs {
		out.card += andRange(&out.words, &bc.words, int(iv.start), int(iv.last))
	}
	if out.card <= arrayMaxSize {
		return out.toArray()
	}
	return out
}

// andRange sets dst = src & mask over the inclusive bit range [start, last]
// and returns the number of bits set.
func andRange(dst, src *[bitmapWords]uint64, start, last int) int {
	wi, wj := start>>6, last>>6
	cnt := 0
	for w := wi; w <= wj; w++ {
		lo, hi := 0, 63
		if w == wi {
			lo = start & 63
		}
		if w == wj {
			hi = last & 63
		}
		v := src[w] & bitmask(lo, hi)
		dst[w] = v
		cnt += bits.OnesCount64(v)
	}
	return cnt
}

func intersectRuns(a, b *runContainer) container {
	var res []interval
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		s := maxU16(a.runs[i].start, b.runs[j].start)
		e := minU16(a.runs[i].last, b.runs[j].last)
		if s <= e {
			res = append(res, interval{s, e})
		}
		if a.runs[i].last < b.runs[j].last {
			i++
		} else {
			j++
		}
	}
	rc := &runContainer{runs: res}
	rc.recomputeCard()
	return rc
}

// ---------------------------------------------------------------------------
// OR helpers
// ---------------------------------------------------------------------------

func unionArrays(a, b *arrayContainer) container {
	res := make([]uint16, 0, len(a.data)+len(b.data))
	i, j := 0, 0
	for i < len(a.data) && j < len(b.data) {
		switch {
		case a.data[i] < b.data[j]:
			res = append(res, a.data[i])
			i++
		case a.data[i] > b.data[j]:
			res = append(res, b.data[j])
			j++
		default:
			res = append(res, a.data[i])
			i++
			j++
		}
	}
	res = append(res, a.data[i:]...)
	res = append(res, b.data[j:]...)
	ac := &arrayContainer{data: res}
	if len(res) > arrayMaxSize {
		return ac.toBitmap()
	}
	return ac
}

func unionArrayBitmap(ac *arrayContainer, bc *bitmapContainer) container {
	nb := bc.clone().(*bitmapContainer)
	for _, v := range ac.data {
		nb.set(v)
	}
	return nb
}

func unionArrayRun(ac *arrayContainer, rc *runContainer) container {
	nb := rc.toBitmap()
	for _, v := range ac.data {
		nb.set(v)
	}
	return nb
}

func unionBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range out.words {
		w := a.words[i] | b.words[i]
		out.words[i] = w
		out.card += bits.OnesCount64(w)
	}
	return out
}

func unionBitmapRun(bc *bitmapContainer, rc *runContainer) container {
	nb := bc.clone().(*bitmapContainer)
	for _, iv := range rc.runs {
		nb.setRange(int(iv.start), int(iv.last))
	}
	return nb
}

func unionRuns(a, b *runContainer) container {
	res := make([]interval, 0, len(a.runs)+len(b.runs))
	i, j := 0, 0
	var cur interval
	open := false
	push := func(iv interval) {
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
			res = append(res, cur)
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
	if open {
		res = append(res, cur)
	}
	rc := &runContainer{runs: res}
	rc.recomputeCard()
	if len(res) > runMaxCount {
		return rc.toBitmap()
	}
	return rc
}

// ---------------------------------------------------------------------------
// XOR helpers
// ---------------------------------------------------------------------------

func xorArrays(a, b *arrayContainer) container {
	res := make([]uint16, 0, len(a.data)+len(b.data))
	i, j := 0, 0
	for i < len(a.data) && j < len(b.data) {
		switch {
		case a.data[i] < b.data[j]:
			res = append(res, a.data[i])
			i++
		case a.data[i] > b.data[j]:
			res = append(res, b.data[j])
			j++
		default:
			i++
			j++
		}
	}
	res = append(res, a.data[i:]...)
	res = append(res, b.data[j:]...)
	ac := &arrayContainer{data: res}
	if len(res) > arrayMaxSize {
		return ac.toBitmap()
	}
	return ac
}

func xorArrayBitmap(ac *arrayContainer, bc *bitmapContainer) container {
	nb := bc.clone().(*bitmapContainer)
	for _, v := range ac.data {
		nb.flip(v)
	}
	if nb.card <= arrayMaxSize {
		return nb.toArray()
	}
	return nb
}

// flip toggles the bit for x.
func (bc *bitmapContainer) flip(x uint16) {
	w := &bc.words[x>>6]
	mask := uint64(1) << (x & 63)
	if *w&mask != 0 {
		*w &^= mask
		bc.card--
	} else {
		*w |= mask
		bc.card++
	}
}

func xorArrayRun(ac *arrayContainer, rc *runContainer) container {
	nb := rc.toBitmap()
	for _, v := range ac.data {
		nb.flip(v)
	}
	if nb.card <= arrayMaxSize {
		return nb.toArray()
	}
	return nb
}

func xorBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range out.words {
		w := a.words[i] ^ b.words[i]
		out.words[i] = w
		out.card += bits.OnesCount64(w)
	}
	if out.card <= arrayMaxSize {
		return out.toArray()
	}
	return out
}

func xorBitmapRun(bc *bitmapContainer, rc *runContainer) container {
	nb := bc.clone().(*bitmapContainer)
	for _, iv := range rc.runs {
		nb.flipRange(int(iv.start), int(iv.last))
	}
	if nb.card <= arrayMaxSize {
		return nb.toArray()
	}
	return nb
}

func xorRuns(a, b *runContainer) container {
	type event struct {
		pos   int
		delta int
	}
	events := make([]event, 0, 2*(len(a.runs)+len(b.runs)))
	for _, r := range a.runs {
		events = append(events, event{int(r.start), 1}, event{int(r.last) + 1, -1})
	}
	for _, r := range b.runs {
		events = append(events, event{int(r.start), 1}, event{int(r.last) + 1, -1})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].pos < events[j].pos })

	var raw []interval
	depth := 0
	start := -1
	for _, e := range events {
		wasActive := depth%2 == 1
		depth += e.delta
		active := depth%2 == 1
		switch {
		case !wasActive && active:
			start = e.pos
		case wasActive && !active:
			if start >= 0 && start <= e.pos-1 {
				raw = append(raw, interval{uint16(start), uint16(e.pos - 1)})
			}
		}
	}
	// Merge adjacent runs that may have been split at shared boundaries.
	res := raw[:0]
	for _, iv := range raw {
		if n := len(res); n > 0 && int(iv.start) <= int(res[n-1].last)+1 {
			if iv.last > res[n-1].last {
				res[n-1].last = iv.last
			}
		} else {
			res = append(res, iv)
		}
	}
	rc := &runContainer{runs: res}
	rc.recomputeCard()
	if len(res) > runMaxCount {
		return rc.toBitmap()
	}
	return rc
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func minInt(a, b int) int {
	if a < b {
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

func maxU16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}
