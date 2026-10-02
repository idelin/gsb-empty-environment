package roaring

import "math/bits"

// ---------------------------------------------------------------------------
// container-level boolean operations. All return nil for an empty result and
// always return freshly allocated containers (inputs are never mutated).
// ---------------------------------------------------------------------------

func containerAnd(a, b container) container {
	if ac, ok := a.(*arrayContainer); ok {
		return andArrayOther(ac, b)
	}
	if bc, ok := b.(*arrayContainer); ok {
		return andArrayOther(bc, a)
	}
	if ar, ok := a.(*runContainer); ok {
		if br, ok := b.(*runContainer); ok {
			return repair(intersectRuns(ar, br))
		}
	}
	return repair(andBitmaps(a.toBitmap(), b.toBitmap()))
}

func containerOr(a, b container) container {
	if ac, ok := a.(*arrayContainer); ok {
		if bc, ok := b.(*arrayContainer); ok {
			return repair(unionArrays(ac, bc))
		}
	}
	if ar, ok := a.(*runContainer); ok {
		if br, ok := b.(*runContainer); ok {
			return repair(unionRuns(ar, br))
		}
	}
	return repair(orBitmaps(a.toBitmap(), b.toBitmap()))
}

func containerXor(a, b container) container {
	if ac, ok := a.(*arrayContainer); ok {
		if bc, ok := b.(*arrayContainer); ok {
			return repair(xorArrays(ac, bc))
		}
	}
	return repair(xorBitmaps(a.toBitmap(), b.toBitmap()))
}

// ---------------------------------------------------------------------------
// array helpers
// ---------------------------------------------------------------------------

func intersectArrays(a, b *arrayContainer) container {
	n := intersectArraysCount(a.vals, b.vals)
	if n == 0 {
		return nil
	}
	vals := make([]uint16, 0, n)
	i, j := 0, 0
	for i < len(a.vals) && j < len(b.vals) {
		switch {
		case a.vals[i] < b.vals[j]:
			i++
		case a.vals[i] > b.vals[j]:
			j++
		default:
			vals = append(vals, a.vals[i])
			i++
			j++
		}
	}
	return &arrayContainer{vals: vals}
}

func intersectArraysCount(a, b []uint16) int {
	n, i, j := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			n++
			i++
			j++
		}
	}
	return n
}

func andArrayOther(ac *arrayContainer, other container) container {
	if oc, ok := other.(*arrayContainer); ok {
		return intersectArrays(ac, oc)
	}
	n := 0
	for _, v := range ac.vals {
		if other.contains(v) {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	vals := make([]uint16, 0, n)
	for _, v := range ac.vals {
		if other.contains(v) {
			vals = append(vals, v)
		}
	}
	return &arrayContainer{vals: vals}
}

func unionArrays(a, b *arrayContainer) container {
	vals := make([]uint16, 0, len(a.vals)+len(b.vals))
	i, j := 0, 0
	for i < len(a.vals) && j < len(b.vals) {
		switch {
		case a.vals[i] < b.vals[j]:
			vals = append(vals, a.vals[i])
			i++
		case a.vals[i] > b.vals[j]:
			vals = append(vals, b.vals[j])
			j++
		default:
			vals = append(vals, a.vals[i])
			i++
			j++
		}
	}
	vals = append(vals, a.vals[i:]...)
	vals = append(vals, b.vals[j:]...)
	return &arrayContainer{vals: vals}
}

func xorArrays(a, b *arrayContainer) container {
	vals := make([]uint16, 0, len(a.vals)+len(b.vals))
	i, j := 0, 0
	for i < len(a.vals) && j < len(b.vals) {
		switch {
		case a.vals[i] < b.vals[j]:
			vals = append(vals, a.vals[i])
			i++
		case a.vals[i] > b.vals[j]:
			vals = append(vals, b.vals[j])
			j++
		default:
			i++
			j++
		}
	}
	vals = append(vals, a.vals[i:]...)
	vals = append(vals, b.vals[j:]...)
	return &arrayContainer{vals: vals}
}

// ---------------------------------------------------------------------------
// bitmap helpers
// ---------------------------------------------------------------------------

func andBitmaps(a, b *bitmapContainer) *bitmapContainer {
	out := &bitmapContainer{words: make([]uint64, bitmapWords)}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.words[i] & b.words[i]
		out.words[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return out
}

func orBitmaps(a, b *bitmapContainer) *bitmapContainer {
	out := &bitmapContainer{words: make([]uint64, bitmapWords)}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.words[i] | b.words[i]
		out.words[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return out
}

func xorBitmaps(a, b *bitmapContainer) *bitmapContainer {
	out := &bitmapContainer{words: make([]uint64, bitmapWords)}
	card := 0
	for i := 0; i < bitmapWords; i++ {
		w := a.words[i] ^ b.words[i]
		out.words[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	return out
}

// ---------------------------------------------------------------------------
// run helpers
// ---------------------------------------------------------------------------

func intersectRuns(a, b *runContainer) *runContainer {
	out := &runContainer{}
	i, j := 0, 0
	for i < len(a.iv) && j < len(b.iv) {
		ai, bi := a.iv[i], b.iv[j]
		aStart, aEnd := int(ai.start), int(ai.start)+int(ai.length)
		bStart, bEnd := int(bi.start), int(bi.start)+int(bi.length)
		start := maxInt(aStart, bStart)
		end := minInt(aEnd, bEnd)
		if start <= end {
			out.iv = append(out.iv, runInterval{start: uint16(start), length: uint16(end - start)})
		}
		if aEnd < bEnd {
			i++
		} else {
			j++
		}
	}
	return out
}

func unionRuns(a, b *runContainer) *runContainer {
	out := &runContainer{iv: make([]runInterval, 0, len(a.iv)+len(b.iv))}
	i, j := 0, 0
	var cur runInterval
	have := false
	push := func(iv runInterval) {
		if !have {
			cur = iv
			have = true
			return
		}
		curEnd := int(cur.start) + int(cur.length)
		ivStart := int(iv.start)
		if ivStart <= curEnd+1 {
			if ivEnd := ivStart + int(iv.length); ivEnd > curEnd {
				cur.length = uint16(ivEnd - int(cur.start))
			}
		} else {
			out.iv = append(out.iv, cur)
			cur = iv
		}
	}
	for i < len(a.iv) && j < len(b.iv) {
		if a.iv[i].start <= b.iv[j].start {
			push(a.iv[i])
			i++
		} else {
			push(b.iv[j])
			j++
		}
	}
	for ; i < len(a.iv); i++ {
		push(a.iv[i])
	}
	for ; j < len(b.iv); j++ {
		push(b.iv[j])
	}
	if have {
		out.iv = append(out.iv, cur)
	}
	return out
}

// ---------------------------------------------------------------------------
// Bitmap-level boolean operations
// ---------------------------------------------------------------------------

// And returns the intersection of b and o. Inputs are not modified.
func (b *Bitmap) And(o *Bitmap) *Bitmap {
	out := &Bitmap{entries: make([]bitmapEntry, 0, minInt(len(b.entries), len(o.entries)))}
	i, j := 0, 0
	for i < len(b.entries) && j < len(o.entries) {
		ei, ej := &b.entries[i], &o.entries[j]
		switch {
		case ei.key < ej.key:
			i++
		case ei.key > ej.key:
			j++
		default:
			if c := containerAnd(ei.c, ej.c); c != nil {
				out.entries = append(out.entries, bitmapEntry{key: ei.key, c: c})
			}
			i++
			j++
		}
	}
	return out
}

// Or returns the union of b and o. Inputs are not modified.
func (b *Bitmap) Or(o *Bitmap) *Bitmap {
	out := &Bitmap{entries: make([]bitmapEntry, 0, len(b.entries)+len(o.entries))}
	i, j := 0, 0
	for i < len(b.entries) && j < len(o.entries) {
		ei, ej := &b.entries[i], &o.entries[j]
		switch {
		case ei.key < ej.key:
			out.entries = append(out.entries, bitmapEntry{key: ei.key, c: ei.c.clone()})
			i++
		case ei.key > ej.key:
			out.entries = append(out.entries, bitmapEntry{key: ej.key, c: ej.c.clone()})
			j++
		default:
			if c := containerOr(ei.c, ej.c); c != nil {
				out.entries = append(out.entries, bitmapEntry{key: ei.key, c: c})
			}
			i++
			j++
		}
	}
	for ; i < len(b.entries); i++ {
		out.entries = append(out.entries, bitmapEntry{key: b.entries[i].key, c: b.entries[i].c.clone()})
	}
	for ; j < len(o.entries); j++ {
		out.entries = append(out.entries, bitmapEntry{key: o.entries[j].key, c: o.entries[j].c.clone()})
	}
	return out
}

// Xor returns the symmetric difference of b and o. Inputs are not modified.
func (b *Bitmap) Xor(o *Bitmap) *Bitmap {
	out := &Bitmap{entries: make([]bitmapEntry, 0, len(b.entries)+len(o.entries))}
	i, j := 0, 0
	for i < len(b.entries) && j < len(o.entries) {
		ei, ej := &b.entries[i], &o.entries[j]
		switch {
		case ei.key < ej.key:
			out.entries = append(out.entries, bitmapEntry{key: ei.key, c: ei.c.clone()})
			i++
		case ei.key > ej.key:
			out.entries = append(out.entries, bitmapEntry{key: ej.key, c: ej.c.clone()})
			j++
		default:
			if c := containerXor(ei.c, ej.c); c != nil {
				out.entries = append(out.entries, bitmapEntry{key: ei.key, c: c})
			}
			i++
			j++
		}
	}
	for ; i < len(b.entries); i++ {
		out.entries = append(out.entries, bitmapEntry{key: b.entries[i].key, c: b.entries[i].c.clone()})
	}
	for ; j < len(o.entries); j++ {
		out.entries = append(out.entries, bitmapEntry{key: o.entries[j].key, c: o.entries[j].c.clone()})
	}
	return out
}

// AndAll returns the intersection of all given bitmaps (batch AND).
func AndAll(bitmaps ...*Bitmap) *Bitmap {
	if len(bitmaps) == 0 {
		return New()
	}
	out := bitmaps[0]
	for _, b := range bitmaps[1:] {
		out = out.And(b)
	}
	return out
}

// OrAll returns the union of all given bitmaps (batch OR).
func OrAll(bitmaps ...*Bitmap) *Bitmap {
	out := New()
	for _, b := range bitmaps {
		out = out.Or(b)
	}
	return out
}

// XorAll returns the symmetric difference of all given bitmaps (batch XOR).
func XorAll(bitmaps ...*Bitmap) *Bitmap {
	out := New()
	for _, b := range bitmaps {
		out = out.Xor(b)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
