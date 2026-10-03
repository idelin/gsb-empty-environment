package bitmap

// Container-level set operations. Every function is pure: inputs are never
// mutated, results are freshly allocated and returned in the cheapest
// representation via optimize().

func minu16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

func maxu16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}

// andContainers returns a new container with the intersection of a and b.
func andContainers(a, b container) container {
	return andDispatch(a, b).optimize()
}

// orContainers returns a new container with the union of a and b.
func orContainers(a, b container) container {
	return orDispatch(a, b).optimize()
}

// xorContainers returns a new container with the symmetric difference of a and b.
func xorContainers(a, b container) container {
	return xorDispatch(a, b).optimize()
}

func andDispatch(a, b container) container {
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

func orDispatch(a, b container) container {
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

func xorDispatch(a, b container) container {
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

// --- array vs array ---

func andArrayArray(a, b *arrayContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, 16)}
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
	return out
}

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
	return out
}

// --- array vs bitmap ---

func andArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, 16)}
	for _, v := range a.data {
		if b.contains(v) {
			out.data = append(out.data, v)
		}
	}
	return out
}

func orArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, v := range a.data {
		out.add(v)
	}
	return out
}

func xorArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	var cur container = b.clone()
	for _, v := range a.data {
		if cur.contains(v) {
			cur, _ = cur.remove(v)
		} else {
			cur, _ = cur.add(v)
		}
	}
	return cur
}

// --- array vs run ---

func andArrayRun(a *arrayContainer, r *runContainer) container {
	out := &arrayContainer{data: make([]uint16, 0, 16)}
	data := a.data
	for _, iv := range r.runs {
		lo := 0
		for lo < len(data) && data[lo] < iv.start {
			lo++
		}
		hi := lo
		for hi < len(data) && data[hi] <= iv.last() {
			hi++
		}
		out.data = append(out.data, data[lo:hi]...)
		data = data[hi:]
		if len(data) == 0 {
			break
		}
	}
	return out
}

func orArrayRun(a *arrayContainer, r *runContainer) container {
	var cur container = r.clone()
	for _, v := range a.data {
		cur, _ = cur.add(v)
	}
	return cur
}

func xorArrayRun(a *arrayContainer, r *runContainer) container {
	var cur container = r.clone()
	for _, v := range a.data {
		if cur.contains(v) {
			cur, _ = cur.remove(v)
		} else {
			cur, _ = cur.add(v)
		}
	}
	return cur
}

// --- bitmap vs bitmap ---

func andBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range out.words {
		w := a.words[i] & b.words[i]
		out.words[i] = w
		out.card += popcount(w)
	}
	return out
}

func orBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range out.words {
		w := a.words[i] | b.words[i]
		out.words[i] = w
		out.card += popcount(w)
	}
	return out
}

func xorBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range out.words {
		w := a.words[i] ^ b.words[i]
		out.words[i] = w
		out.card += popcount(w)
	}
	return out
}

// --- bitmap vs run ---

func andBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := &bitmapContainer{}
	for _, iv := range r.runs {
		w0, w1 := int(iv.start)>>6, int(iv.last())>>6
		for wi := w0; wi <= w1; wi++ {
			var lo, hi uint64 = 0, 63
			if wi == w0 {
				lo = uint64(iv.start & 63)
			}
			if wi == w1 {
				hi = uint64(iv.last() & 63)
			}
			// Adjacent runs may share a word, so accumulate with OR.
			w := b.words[wi] & rangeMask(lo, hi)
			out.card -= popcount(out.words[wi])
			out.words[wi] |= w
			out.card += popcount(out.words[wi])
		}
	}
	return out
}

func orBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, iv := range r.runs {
		out.setRange(iv.start, iv.last(), false)
	}
	out.recount()
	return out
}

func xorBitmapRun(b *bitmapContainer, r *runContainer) container {
	out := b.clone().(*bitmapContainer)
	for _, iv := range r.runs {
		out.setRange(iv.start, iv.last(), true)
	}
	out.recount()
	return out
}

// --- run vs run ---

func andRunRun(a, b *runContainer) container {
	out := &runContainer{}
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		ra, rb := a.runs[i], b.runs[j]
		s := maxu16(ra.start, rb.start)
		l := minu16(ra.last(), rb.last())
		if s <= l {
			out.runs = append(out.runs, interval{s, l - s})
		}
		if ra.last() < rb.last() {
			i++
		} else {
			j++
		}
	}
	return out
}

func orRunRun(a, b *runContainer) container {
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
		if int(iv.start) <= int(cur.last())+1 {
			if iv.last() > cur.last() {
				cur.length = iv.last() - cur.start
			}
			return
		}
		out.runs = append(out.runs, cur)
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
	if have {
		out.runs = append(out.runs, cur)
	}
	return out
}

func xorRunRun(a, b *runContainer) container {
	// Symmetric difference = (a-b) merged with (b-a); both are sorted and
	// mutually disjoint, so a union merge (which also coalesces adjacency)
	// yields the canonical run list. O(runs), no bitmap materialization.
	d1 := &runContainer{runs: subtractRuns(a.runs, b.runs)}
	d2 := &runContainer{runs: subtractRuns(b.runs, a.runs)}
	return orRunRun(d1, d2)
}

// subtractRuns returns the sorted intervals of a with all b intervals cut
// out. Inputs must be canonical (sorted, non-overlapping).
func subtractRuns(a, b []interval) []interval {
	out := make([]interval, 0, len(a))
	j := 0
	for _, iv := range a {
		s, e := uint32(iv.start), uint32(iv.last())
		for j < len(b) && uint32(b[j].last()) < s {
			j++
		}
		k := j
		for k < len(b) && uint32(b[k].start) <= e && s <= e {
			bs, be := uint32(b[k].start), uint32(b[k].last())
			if bs > s {
				out = append(out, interval{uint16(s), uint16(bs - 1 - s)})
			}
			if be >= e {
				s = e + 1
				break
			}
			s = be + 1
			k++
		}
		if s <= e {
			out = append(out, interval{uint16(s), uint16(e - s)})
		}
	}
	return out
}
