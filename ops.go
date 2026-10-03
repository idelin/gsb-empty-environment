package bitmapckpt

import "math/bits"

// Container-level boolean operations. Inputs are never mutated (they may be
// shared with snapshots); every operation returns a fresh, normalized
// container.

func andContainers(a, b container) container {
	ta, tb := a.typeID(), b.typeID()
	if ta > tb {
		a, b, ta, tb = b, a, tb, ta
	}
	switch {
	case ta == typeArray && tb == typeArray:
		return andArrayArray(a.(*arrayContainer), b.(*arrayContainer))
	case ta == typeArray && tb == typeBitmap:
		return andArrayBitmap(a.(*arrayContainer), b.(*bitmapContainer))
	case ta == typeArray && tb == typeRun:
		return andArrayRun(a.(*arrayContainer), b.(*runContainer))
	case ta == typeBitmap && tb == typeBitmap:
		return andBitmapBitmap(a.(*bitmapContainer), b.(*bitmapContainer))
	case ta == typeBitmap && tb == typeRun:
		return andBitmapRun(a.(*bitmapContainer), b.(*runContainer))
	default:
		return andRunRun(a.(*runContainer), b.(*runContainer))
	}
}

func orContainers(a, b container) container {
	ta, tb := a.typeID(), b.typeID()
	if ta > tb {
		a, b, ta, tb = b, a, tb, ta
	}
	switch {
	case ta == typeArray && tb == typeArray:
		return orArrayArray(a.(*arrayContainer), b.(*arrayContainer))
	case ta == typeArray && tb == typeBitmap:
		return orArrayBitmap(a.(*arrayContainer), b.(*bitmapContainer))
	case ta == typeArray && tb == typeRun:
		return orArrayRun(a.(*arrayContainer), b.(*runContainer))
	case ta == typeBitmap && tb == typeBitmap:
		return orBitmapBitmap(a.(*bitmapContainer), b.(*bitmapContainer))
	case ta == typeBitmap && tb == typeRun:
		return orBitmapRun(a.(*bitmapContainer), b.(*runContainer))
	default:
		return orRunRun(a.(*runContainer), b.(*runContainer))
	}
}

func xorContainers(a, b container) container {
	ta, tb := a.typeID(), b.typeID()
	if ta > tb {
		a, b, ta, tb = b, a, tb, ta
	}
	switch {
	case ta == typeArray && tb == typeArray:
		return xorArrayArray(a.(*arrayContainer), b.(*arrayContainer))
	case ta == typeArray && tb == typeBitmap:
		return xorArrayBitmap(a.(*arrayContainer), b.(*bitmapContainer))
	case ta == typeArray && tb == typeRun:
		return xorArrayRun(a.(*arrayContainer), b.(*runContainer))
	case ta == typeBitmap && tb == typeBitmap:
		return xorBitmapBitmap(a.(*bitmapContainer), b.(*bitmapContainer))
	case ta == typeBitmap && tb == typeRun:
		return xorBitmapRun(a.(*bitmapContainer), b.(*runContainer))
	default:
		return xorRunRun(a.(*runContainer), b.(*runContainer))
	}
}

// ---------------------------------------------------------------------------
// AND
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

func andArrayRun(a *arrayContainer, r *runContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, len(a.data))}
	for _, v := range a.data {
		if r.contains(v) {
			out.data = append(out.data, v)
		}
	}
	return out
}

func andBitmapBitmap(a, b *bitmapContainer) container {
	res := &bitmapContainer{}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.words[i] & b.words[i]
		res.words[i] = w
		card += bits.OnesCount64(w)
	}
	res.card = card
	return normalizeBitmap(res)
}

func andBitmapRun(b *bitmapContainer, r *runContainer) container {
	res := &bitmapContainer{}
	for _, iv := range r.runs {
		for w := int(iv.start >> 6); w <= int(iv.last>>6); w++ {
			res.words[w] |= b.words[w] & rangeMask(w, iv.start, iv.last)
		}
	}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		card += bits.OnesCount64(res.words[i])
	}
	res.card = card
	return normalizeBitmap(res)
}

func andRunRun(a, b *runContainer) container {
	out := &runContainer{}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		s := a.runs[i].start
		if b.runs[j].start > s {
			s = b.runs[j].start
		}
		e := a.runs[i].last
		if b.runs[j].last < e {
			e = b.runs[j].last
		}
		if s <= e {
			out.runs = append(out.runs, interval{s, e})
		}
		if a.runs[i].last < b.runs[j].last {
			i++
		} else {
			j++
		}
	}
	out.card = cardOfRuns(out.runs)
	return normalizeRun(out)
}

// ---------------------------------------------------------------------------
// OR
// ---------------------------------------------------------------------------

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
		return out.toBitmap()
	}
	return out
}

func orArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	nb := b.clone().(*bitmapContainer)
	for _, v := range a.data {
		w := v >> 6
		mask := uint64(1) << (v & 63)
		if nb.words[w]&mask == 0 {
			nb.words[w] |= mask
			nb.card++
		}
	}
	return normalizeBitmap(nb)
}

func orArrayRun(a *arrayContainer, r *runContainer) container {
	nr := r.clone().(*runContainer)
	var c container = nr
	for _, v := range a.data {
		c = c.add(v)
	}
	if rc, ok := c.(*runContainer); ok {
		return normalizeRun(rc)
	}
	return c
}

func orBitmapBitmap(a, b *bitmapContainer) container {
	res := &bitmapContainer{}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.words[i] | b.words[i]
		res.words[i] = w
		card += bits.OnesCount64(w)
	}
	res.card = card
	return normalizeBitmap(res)
}

func orBitmapRun(b *bitmapContainer, r *runContainer) container {
	nb := b.clone().(*bitmapContainer)
	for _, iv := range r.runs {
		for w := int(iv.start >> 6); w <= int(iv.last>>6); w++ {
			mask := rangeMask(w, iv.start, iv.last)
			old := nb.words[w]
			nb.card += bits.OnesCount64(mask &^ old)
			nb.words[w] = old | mask
		}
	}
	return normalizeBitmap(nb)
}

func orRunRun(a, b *runContainer) container {
	out := &runContainer{}
	i, j := 0, 0
	var cur interval
	open := false
	flush := func() {
		if open {
			out.runs = append(out.runs, cur)
		}
	}
	push := func(iv interval) {
		if !open {
			cur, open = iv, true
			return
		}
		if uint32(iv.start) <= uint32(cur.last)+1 {
			if iv.last > cur.last {
				cur.last = iv.last
			}
			return
		}
		flush()
		cur = iv
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
	flush()
	out.card = cardOfRuns(out.runs)
	return normalizeRun(out)
}

// ---------------------------------------------------------------------------
// XOR
// ---------------------------------------------------------------------------

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
		return out.toBitmap()
	}
	return out
}

func xorArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	nb := b.clone().(*bitmapContainer)
	for _, v := range a.data {
		w := v >> 6
		mask := uint64(1) << (v & 63)
		if nb.words[w]&mask == 0 {
			nb.card++
		} else {
			nb.card--
		}
		nb.words[w] ^= mask
	}
	return normalizeBitmap(nb)
}

func xorArrayRun(a *arrayContainer, r *runContainer) container {
	nr := r.clone().(*runContainer)
	for _, v := range a.data {
		nr.flipRange(v, v)
	}
	return normalizeRun(nr)
}

func xorBitmapBitmap(a, b *bitmapContainer) container {
	res := &bitmapContainer{}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.words[i] ^ b.words[i]
		res.words[i] = w
		card += bits.OnesCount64(w)
	}
	res.card = card
	return normalizeBitmap(res)
}

func xorBitmapRun(b *bitmapContainer, r *runContainer) container {
	nb := b.clone().(*bitmapContainer)
	for _, iv := range r.runs {
		for w := int(iv.start >> 6); w <= int(iv.last>>6); w++ {
			mask := rangeMask(w, iv.start, iv.last)
			old := nb.words[w]
			nb.card += bits.OnesCount64(mask) - 2*bits.OnesCount64(old&mask)
			nb.words[w] = old ^ mask
		}
	}
	return normalizeBitmap(nb)
}

func xorRunRun(a, b *runContainer) container {
	nr := a.clone().(*runContainer)
	for _, iv := range b.runs {
		nr.flipRange(iv.start, iv.last)
	}
	return normalizeRun(nr)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
