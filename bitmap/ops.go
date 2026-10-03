package bitmap

import "math/bits"

// andContainers computes the intersection of two containers of any kind.
func andContainers(a, b container) container {
	switch ta := a.(type) {
	case *bitmapContainer:
		if tb, ok := b.(*bitmapContainer); ok {
			return andBitmapBitmap(ta, tb)
		}
	case *runContainer:
		if tb, ok := b.(*runContainer); ok {
			return andRunRun(ta, tb)
		}
	case *arrayContainer:
		if tb, ok := b.(*arrayContainer); ok {
			return andArrayArray(ta, tb)
		}
	}
	// Generic mixed-type path: iterate the smaller set, probe the larger.
	small, big := a, b
	if b.cardinality() < a.cardinality() {
		small, big = b, a
	}
	out := &arrayContainer{}
	for _, v := range small.toSlice() {
		if big.contains(v) {
			out.data = append(out.data, v)
		}
	}
	if len(out.data) > arrayMaxCardinality {
		return out.toBitmap()
	}
	return out
}

// orContainers computes the union of two containers of any kind.
func orContainers(a, b container) container {
	switch ta := a.(type) {
	case *bitmapContainer:
		if tb, ok := b.(*bitmapContainer); ok {
			return orBitmapBitmap(ta, tb)
		}
	case *runContainer:
		if tb, ok := b.(*runContainer); ok {
			return orRunRun(ta, tb)
		}
	case *arrayContainer:
		if tb, ok := b.(*arrayContainer); ok {
			return orArrayArray(ta, tb)
		}
	}
	// Generic mixed-type path: clone the larger, insert the smaller's values.
	big, small := a, b
	if b.cardinality() > a.cardinality() {
		big, small = b, a
	}
	res := big.clone()
	for _, v := range small.toSlice() {
		res = res.add(v)
	}
	return res.optimize()
}

// xorContainers computes the symmetric difference of two containers.
func xorContainers(a, b container) container {
	if ta, ok := a.(*bitmapContainer); ok {
		if tb, ok := b.(*bitmapContainer); ok {
			return xorBitmapBitmap(ta, tb)
		}
	}
	if ta, ok := a.(*runContainer); ok {
		if tb, ok := b.(*runContainer); ok {
			return xorRunRun(ta, tb)
		}
	}
	// Generic path: merge the two sorted value lists, dropping duplicates.
	sa, sb := a.toSlice(), b.toSlice()
	out := &arrayContainer{data: make([]uint16, 0, len(sa)+len(sb))}
	i, j := 0, 0
	for i < len(sa) && j < len(sb) {
		switch {
		case sa[i] < sb[j]:
			out.data = append(out.data, sa[i])
			i++
		case sa[i] > sb[j]:
			out.data = append(out.data, sb[j])
			j++
		default:
			i++
			j++
		}
	}
	out.data = append(out.data, sa[i:]...)
	out.data = append(out.data, sb[j:]...)
	if len(out.data) > arrayMaxCardinality {
		return out.toBitmap()
	}
	return out
}

func andBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	card := 0
	for i := range a.words {
		w := a.words[i] & b.words[i]
		out.words[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	if card <= arrayMaxCardinality {
		return out.toArray()
	}
	return out
}

func orBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	card := 0
	for i := range a.words {
		w := a.words[i] | b.words[i]
		out.words[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	if card <= arrayMaxCardinality {
		return out.toArray()
	}
	return out
}

func xorBitmapBitmap(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	card := 0
	for i := range a.words {
		w := a.words[i] ^ b.words[i]
		out.words[i] = w
		card += bits.OnesCount64(w)
	}
	out.card = card
	if card <= arrayMaxCardinality {
		return out.toArray()
	}
	return out
}

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
	if len(out.data) > arrayMaxCardinality {
		return out.toBitmap()
	}
	return out
}

func andRunRun(a, b *runContainer) container {
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
			out.runs = append(out.runs, run{start, last})
		}
		if ra.last < rb.last {
			i++
		} else {
			j++
		}
	}
	if len(out.runs) == 0 {
		return &arrayContainer{}
	}
	return out.optimize()
}

func orRunRun(a, b *runContainer) container {
	out := &runContainer{runs: make([]run, 0, len(a.runs)+len(b.runs))}
	i, j := 0, 0
	push := func(r run) {
		if n := len(out.runs); n > 0 && int(r.start) <= int(out.runs[n-1].last)+1 {
			if r.last > out.runs[n-1].last {
				out.runs[n-1].last = r.last
			}
			return
		}
		out.runs = append(out.runs, r)
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
	return out.optimize()
}

// runSubtract returns the sorted intervals of a that do not overlap b.
func runSubtract(a, b *runContainer) []run {
	var out []run
	j := 0
	for _, ra := range a.runs {
		cur := ra
		for j < len(b.runs) && b.runs[j].last < cur.start {
			j++
		}
		for k := j; k < len(b.runs) && b.runs[k].start <= cur.last; k++ {
			rb := b.runs[k]
			if rb.start > cur.start {
				out = append(out, run{cur.start, rb.start - 1})
			}
			if rb.last >= cur.last {
				cur = run{1, 0} // empty
				break
			}
			cur.start = rb.last + 1
		}
		if cur.start <= cur.last {
			out = append(out, cur)
		}
	}
	return out
}

// xorRunRun computes the symmetric difference of two run containers
// directly at the interval level, without materializing values.
func xorRunRun(a, b *runContainer) container {
	d1 := runSubtract(a, b)
	d2 := runSubtract(b, a)
	out := &runContainer{runs: make([]run, 0, len(d1)+len(d2))}
	push := func(r run) {
		if n := len(out.runs); n > 0 && int(r.start) <= int(out.runs[n-1].last)+1 {
			if r.last > out.runs[n-1].last {
				out.runs[n-1].last = r.last
			}
			return
		}
		out.runs = append(out.runs, r)
	}
	i, j := 0, 0
	for i < len(d1) && j < len(d2) {
		if d1[i].start <= d2[j].start {
			push(d1[i])
			i++
		} else {
			push(d2[j])
			j++
		}
	}
	for ; i < len(d1); i++ {
		push(d1[i])
	}
	for ; j < len(d2); j++ {
		push(d2[j])
	}
	if len(out.runs) == 0 {
		return &arrayContainer{}
	}
	return out.optimize()
}
