package bitmap

import "math/bits"

// 本文件实现三种容器两两之间的 AND / OR / XOR。
// 返回的容器未做最优归一化，调用方负责按需调用 optimize。

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
	panic("bitmap: unknown container type")
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
	panic("bitmap: unknown container type")
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
			return xorArrayRun(x, y)
		}
	case *bitmapContainer:
		switch y := b.(type) {
		case *arrayContainer:
			return xorArrayBitmap(y, x)
		case *bitmapContainer:
			return xorBitmaps(x, y)
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
			return xorRuns(x, y)
		}
	}
	panic("bitmap: unknown container type")
}

// ---------------- AND ----------------

func intersectArrays(a, b *arrayContainer) container {
	out := make([]uint16, 0, min(len(a.values), len(b.values)))
	i, j := 0, 0
	for i < len(a.values) && j < len(b.values) {
		switch {
		case a.values[i] == b.values[j]:
			out = append(out, a.values[i])
			i++
			j++
		case a.values[i] < b.values[j]:
			i++
		default:
			j++
		}
	}
	return &arrayContainer{values: out}
}

func intersectArrayBitmap(a *arrayContainer, bc *bitmapContainer) container {
	out := make([]uint16, 0, len(a.values))
	for _, v := range a.values {
		if bc.contains(v) {
			out = append(out, v)
		}
	}
	return &arrayContainer{values: out}
}

func intersectArrayRun(a *arrayContainer, rc *runContainer) container {
	out := make([]uint16, 0, len(a.values))
	for _, v := range a.values {
		if rc.contains(v) {
			out = append(out, v)
		}
	}
	return &arrayContainer{values: out}
}

func intersectBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range a.words {
		w := a.words[i] & b.words[i]
		out.words[i] = w
		out.card += bits.OnesCount64(w)
	}
	return out
}

func intersectBitmapRun(bc *bitmapContainer, rc *runContainer) container {
	return intersectBitmaps(bc, toBitmapContainer(rc))
}

func intersectRuns(a, b *runContainer) container {
	out := make([]interval, 0, min(len(a.runs), len(b.runs)))
	i, j := 0, 0
	for i < len(a.runs) && j < len(b.runs) {
		lo := max(a.runs[i].start, b.runs[j].start)
		hi := min(a.runs[i].last, b.runs[j].last)
		if lo <= hi {
			out = append(out, interval{start: lo, last: hi})
		}
		if a.runs[i].last < b.runs[j].last {
			i++
		} else {
			j++
		}
	}
	return &runContainer{runs: out}
}

// ---------------- OR ----------------

func unionArrays(a, b *arrayContainer) container {
	out := make([]uint16, 0, len(a.values)+len(b.values))
	i, j := 0, 0
	for i < len(a.values) && j < len(b.values) {
		switch {
		case a.values[i] == b.values[j]:
			out = append(out, a.values[i])
			i++
			j++
		case a.values[i] < b.values[j]:
			out = append(out, a.values[i])
			i++
		default:
			out = append(out, b.values[j])
			j++
		}
	}
	out = append(out, a.values[i:]...)
	out = append(out, b.values[j:]...)
	ac := &arrayContainer{values: out}
	if len(out) > arrayMaxSize {
		return toBitmapContainer(ac)
	}
	return ac
}

func unionArrayBitmap(a *arrayContainer, bc *bitmapContainer) container {
	out := bc.clone().(*bitmapContainer)
	for _, v := range a.values {
		out.words[v>>6] |= uint64(1) << (v & 63)
	}
	out.recomputeCard()
	return out
}

func unionArrayRun(a *arrayContainer, rc *runContainer) container {
	out := toBitmapContainer(rc)
	for _, v := range a.values {
		out.words[v>>6] |= uint64(1) << (v & 63)
	}
	out.recomputeCard()
	return out
}

func unionBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range a.words {
		w := a.words[i] | b.words[i]
		out.words[i] = w
		out.card += bits.OnesCount64(w)
	}
	return out
}

func unionBitmapRun(bc *bitmapContainer, rc *runContainer) container {
	out := bc.clone().(*bitmapContainer)
	for _, iv := range rc.runs {
		bitmapSetRange(out, iv.start, iv.last)
	}
	out.recomputeCard()
	return out
}

func unionRuns(a, b *runContainer) container {
	out := make([]interval, 0, len(a.runs)+len(b.runs))
	var cur interval
	started := false
	push := func(iv interval) {
		if !started {
			cur = iv
			started = true
			return
		}
		if int(iv.start) <= int(cur.last)+1 {
			if iv.last > cur.last {
				cur.last = iv.last
			}
			return
		}
		out = append(out, cur)
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
		out = append(out, cur)
	}
	return &runContainer{runs: out}
}

// ---------------- XOR ----------------

func xorArrays(a, b *arrayContainer) container {
	out := make([]uint16, 0, len(a.values)+len(b.values))
	i, j := 0, 0
	for i < len(a.values) && j < len(b.values) {
		switch {
		case a.values[i] == b.values[j]:
			i++
			j++
		case a.values[i] < b.values[j]:
			out = append(out, a.values[i])
			i++
		default:
			out = append(out, b.values[j])
			j++
		}
	}
	out = append(out, a.values[i:]...)
	out = append(out, b.values[j:]...)
	ac := &arrayContainer{values: out}
	if len(out) > arrayMaxSize {
		return toBitmapContainer(ac)
	}
	return ac
}

func xorArrayBitmap(a *arrayContainer, bc *bitmapContainer) container {
	out := bc.clone().(*bitmapContainer)
	for _, v := range a.values {
		out.words[v>>6] ^= uint64(1) << (v & 63)
	}
	out.recomputeCard()
	return out
}

func xorArrayRun(a *arrayContainer, rc *runContainer) container {
	out := toBitmapContainer(rc)
	for _, v := range a.values {
		out.words[v>>6] ^= uint64(1) << (v & 63)
	}
	out.recomputeCard()
	return out
}

func xorBitmaps(a, b *bitmapContainer) container {
	out := &bitmapContainer{}
	for i := range a.words {
		w := a.words[i] ^ b.words[i]
		out.words[i] = w
		out.card += bits.OnesCount64(w)
	}
	return out
}

func xorBitmapRun(bc *bitmapContainer, rc *runContainer) container {
	out := bc.clone().(*bitmapContainer)
	for _, iv := range rc.runs {
		bitmapXorRange(out, iv.start, iv.last)
	}
	out.recomputeCard()
	return out
}

func xorRuns(a, b *runContainer) container {
	return xorBitmaps(toBitmapContainer(a), toBitmapContainer(b))
}
