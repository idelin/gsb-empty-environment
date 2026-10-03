package bitmap

import "math/bits"

// 容器级集合运算。所有运算均为纯函数：不修改输入，返回新容器
// （必要时自动选择最优容器类型）。
//
// 分发策略：按 ctype 排序后归约为 6 种有序组合：
// (array,array) (array,bitmap) (array,run) (bitmap,bitmap) (bitmap,run) (run,run)

func andContainers(a, b container) container {
	if a.ctype() > b.ctype() {
		a, b = b, a
	}
	switch {
	case a.ctype() == typeArray && b.ctype() == typeArray:
		return andArrayArray(a.(*arrayContainer), b.(*arrayContainer))
	case a.ctype() == typeArray && b.ctype() == typeBitmap:
		return andArrayBitmap(a.(*arrayContainer), b.(*bitmapContainer))
	case a.ctype() == typeArray && b.ctype() == typeRun:
		return andArrayRun(a.(*arrayContainer), b.(*runContainer))
	case a.ctype() == typeBitmap && b.ctype() == typeBitmap:
		return andBitmapBitmap(a.(*bitmapContainer), b.(*bitmapContainer))
	case a.ctype() == typeBitmap && b.ctype() == typeRun:
		return andBitmapRun(b.(*runContainer), a.(*bitmapContainer))
	default:
		return andRunRun(a.(*runContainer), b.(*runContainer))
	}
}

func orContainers(a, b container) container {
	if a.ctype() > b.ctype() {
		a, b = b, a
	}
	switch {
	case a.ctype() == typeArray && b.ctype() == typeArray:
		return orArrayArray(a.(*arrayContainer), b.(*arrayContainer))
	case a.ctype() == typeArray && b.ctype() == typeBitmap:
		return orArrayBitmap(a.(*arrayContainer), b.(*bitmapContainer))
	case a.ctype() == typeArray && b.ctype() == typeRun:
		return orArrayRun(a.(*arrayContainer), b.(*runContainer))
	case a.ctype() == typeBitmap && b.ctype() == typeBitmap:
		return orBitmapBitmap(a.(*bitmapContainer), b.(*bitmapContainer))
	case a.ctype() == typeBitmap && b.ctype() == typeRun:
		return orBitmapRun(a.(*bitmapContainer), b.(*runContainer))
	default:
		return orRunRun(a.(*runContainer), b.(*runContainer))
	}
}

func xorContainers(a, b container) container {
	if a.ctype() > b.ctype() {
		a, b = b, a
	}
	switch {
	case a.ctype() == typeArray && b.ctype() == typeArray:
		return xorArrayArray(a.(*arrayContainer), b.(*arrayContainer))
	case a.ctype() == typeArray && b.ctype() == typeBitmap:
		return xorArrayBitmap(a.(*arrayContainer), b.(*bitmapContainer))
	case a.ctype() == typeArray && b.ctype() == typeRun:
		return xorArrayRun(a.(*arrayContainer), b.(*runContainer))
	case a.ctype() == typeBitmap && b.ctype() == typeBitmap:
		return xorBitmapBitmap(a.(*bitmapContainer), b.(*bitmapContainer))
	case a.ctype() == typeBitmap && b.ctype() == typeRun:
		return xorBitmapRun(a.(*bitmapContainer), b.(*runContainer))
	default:
		return xorRunRun(a.(*runContainer), b.(*runContainer))
	}
}

// ---------------- AND ----------------

func andArrayArray(x, y *arrayContainer) container {
	out := make([]uint16, 0, minInt(len(x.data), len(y.data)))
	i, j := 0, 0
	for i < len(x.data) && j < len(y.data) {
		switch {
		case x.data[i] < y.data[j]:
			i++
		case x.data[i] > y.data[j]:
			j++
		default:
			out = append(out, x.data[i])
			i++
			j++
		}
	}
	return &arrayContainer{data: out}
}

func andArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	out := make([]uint16, 0, len(a.data))
	for _, v := range a.data {
		if b.contains(v) {
			out = append(out, v)
		}
	}
	return &arrayContainer{data: out}
}

func andArrayRun(a *arrayContainer, r *runContainer) container {
	out := make([]uint16, 0, len(a.data))
	for _, v := range a.data {
		if r.contains(v) {
			out = append(out, v)
		}
	}
	return &arrayContainer{data: out}
}

func andBitmapBitmap(x, y *bitmapContainer) container {
	var words [bitmapWords]uint64
	card := 0
	for i := range words {
		w := x.words[i] & y.words[i]
		words[i] = w
		card += bits.OnesCount64(w)
	}
	return finishBitmap(&words, card)
}

func andBitmapRun(r *runContainer, b *bitmapContainer) container {
	if r.cardinality() <= arrayToBitmapThreshold {
		out := make([]uint16, 0, r.cardinality())
		for _, iv := range r.ivs {
			for v := uint32(iv.start); v <= uint32(iv.last); v++ {
				if b.contains(uint16(v)) {
					out = append(out, uint16(v))
				}
			}
		}
		return &arrayContainer{data: out}
	}
	return andBitmapBitmap(r.toBitmap(), b)
}

func andRunRun(x, y *runContainer) container {
	var ivs []interval
	i, j := 0, 0
	for i < len(x.ivs) && j < len(y.ivs) {
		s := maxU16(x.ivs[i].start, y.ivs[j].start)
		e := minU16(x.ivs[i].last, y.ivs[j].last)
		if s <= e {
			ivs = append(ivs, interval{s, e})
		}
		if x.ivs[i].last < y.ivs[j].last {
			i++
		} else {
			j++
		}
	}
	return (&runContainer{ivs: ivs}).maybeConvert()
}

// ---------------- OR ----------------

func orArrayArray(x, y *arrayContainer) container {
	out := make([]uint16, 0, len(x.data)+len(y.data))
	i, j := 0, 0
	for i < len(x.data) && j < len(y.data) {
		switch {
		case x.data[i] < y.data[j]:
			out = append(out, x.data[i])
			i++
		case x.data[i] > y.data[j]:
			out = append(out, y.data[j])
			j++
		default:
			out = append(out, x.data[i])
			i++
			j++
		}
	}
	out = append(out, x.data[i:]...)
	out = append(out, y.data[j:]...)
	a := &arrayContainer{data: out}
	if len(out) > arrayToBitmapThreshold {
		return a.toBitmap()
	}
	return a
}

func orArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	c := b.clone()
	for _, v := range a.data {
		c = c.add(v)
	}
	return c
}

func orArrayRun(a *arrayContainer, r *runContainer) container {
	var c container = r.clone()
	for _, v := range a.data {
		c = c.add(v)
	}
	return c
}

func orBitmapBitmap(x, y *bitmapContainer) container {
	var words [bitmapWords]uint64
	card := 0
	for i := range words {
		w := x.words[i] | y.words[i]
		words[i] = w
		card += bits.OnesCount64(w)
	}
	return finishBitmap(&words, card)
}

func orBitmapRun(b *bitmapContainer, r *runContainer) container {
	return orBitmapBitmap(b, r.toBitmap())
}

func orRunRun(x, y *runContainer) container {
	// 归并两个有序区间列表并合并重叠/相邻区间。
	merged := make([]interval, 0, len(x.ivs)+len(y.ivs))
	i, j := 0, 0
	push := func(iv interval) {
		if n := len(merged); n > 0 && uint32(iv.start) <= uint32(merged[n-1].last)+1 {
			if iv.last > merged[n-1].last {
				merged[n-1].last = iv.last
			}
			return
		}
		merged = append(merged, iv)
	}
	for i < len(x.ivs) && j < len(y.ivs) {
		if x.ivs[i].start <= y.ivs[j].start {
			push(x.ivs[i])
			i++
		} else {
			push(y.ivs[j])
			j++
		}
	}
	for ; i < len(x.ivs); i++ {
		push(x.ivs[i])
	}
	for ; j < len(y.ivs); j++ {
		push(y.ivs[j])
	}
	return (&runContainer{ivs: merged}).maybeConvert()
}

// ---------------- XOR ----------------

func xorArrayArray(x, y *arrayContainer) container {
	out := make([]uint16, 0, len(x.data)+len(y.data))
	i, j := 0, 0
	for i < len(x.data) && j < len(y.data) {
		switch {
		case x.data[i] < y.data[j]:
			out = append(out, x.data[i])
			i++
		case x.data[i] > y.data[j]:
			out = append(out, y.data[j])
			j++
		default:
			i++
			j++
		}
	}
	out = append(out, x.data[i:]...)
	out = append(out, y.data[j:]...)
	a := &arrayContainer{data: out}
	if len(out) > arrayToBitmapThreshold {
		return a.toBitmap()
	}
	return a
}

func xorArrayBitmap(a *arrayContainer, b *bitmapContainer) container {
	var c container = b.clone()
	for _, v := range a.data {
		c = toggle(c, v)
	}
	return c
}

func xorArrayRun(a *arrayContainer, r *runContainer) container {
	var c container = r.clone()
	for _, v := range a.data {
		c = toggle(c, v)
	}
	return c
}

func xorBitmapBitmap(x, y *bitmapContainer) container {
	var words [bitmapWords]uint64
	card := 0
	for i := range words {
		w := x.words[i] ^ y.words[i]
		words[i] = w
		card += bits.OnesCount64(w)
	}
	return finishBitmap(&words, card)
}

func xorBitmapRun(b *bitmapContainer, r *runContainer) container {
	return xorBitmapBitmap(b, r.toBitmap())
}

// xorRunRun 用扫描线在 O(区间数) 内求两个区间集合的对称差，
// 避免 run->bitmap 转换带来的 O(基数) 开销。
func xorRunRun(x, y *runContainer) container {
	var ivs []interval
	i, j := 0, 0
	open := false
	var openStart uint32
	pos := uint32(0)
	for pos < 1<<16 {
		for i < len(x.ivs) && uint32(x.ivs[i].last) < pos {
			i++
		}
		inX := i < len(x.ivs) && uint32(x.ivs[i].start) <= pos
		for j < len(y.ivs) && uint32(y.ivs[j].last) < pos {
			j++
		}
		inY := j < len(y.ivs) && uint32(y.ivs[j].start) <= pos
		in := inX != inY
		if in && !open {
			open = true
			openStart = pos
		} else if !in && open {
			open = false
			ivs = append(ivs, interval{uint16(openStart), uint16(pos - 1)})
		}
		next := uint32(1 << 16)
		if i < len(x.ivs) {
			if inX {
				next = minU32(next, uint32(x.ivs[i].last)+1)
			} else {
				next = minU32(next, uint32(x.ivs[i].start))
			}
		}
		if j < len(y.ivs) {
			if inY {
				next = minU32(next, uint32(y.ivs[j].last)+1)
			} else {
				next = minU32(next, uint32(y.ivs[j].start))
			}
		}
		pos = next
	}
	if open {
		ivs = append(ivs, interval{uint16(openStart), 0xFFFF})
	}
	return (&runContainer{ivs: ivs}).maybeConvert()
}

// ---------------- helpers ----------------

func toggle(c container, v uint16) container {
	if c.contains(v) {
		return c.remove(v)
	}
	return c.add(v)
}

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

func minU32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}
