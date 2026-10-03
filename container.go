package bitmap

import (
	"math/bits"
	"sort"
)

const (
	// arrayToBitmapThreshold 是 ArrayContainer 允许的最大基数，
	// 超过后自动升级为 BitmapContainer（与 Roaring Bitmap 一致）。
	arrayToBitmapThreshold = 4096
	bitmapWords            = 1024 // 65536 bit = 1024 x uint64
	bitmapBytes            = bitmapWords * 8
)

// 容器类型标签，用于序列化与运行时分发。
const (
	typeArray  byte = 0
	typeBitmap byte = 1
	typeRun    byte = 2
)

// container 是位图低 16 位的存储容器。
// 约定：add/remove 允许就地修改接收者，调用方（Bitmap）负责
// 在容器被快照共享时先 clone（Copy-on-Write）。
type container interface {
	contains(v uint16) bool
	cardinality() int
	add(v uint16) container
	remove(v uint16) container
	clone() container
	toSlice() []uint16
	ctype() byte
}

// ---------------- ArrayContainer ----------------

type arrayContainer struct{ data []uint16 } // 始终有序

func (a *arrayContainer) ctype() byte      { return typeArray }
func (a *arrayContainer) cardinality() int { return len(a.data) }

func (a *arrayContainer) contains(v uint16) bool {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	return i < len(a.data) && a.data[i] == v
}

func (a *arrayContainer) add(v uint16) container {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	if i < len(a.data) && a.data[i] == v {
		return a
	}
	a.data = append(a.data, 0)
	copy(a.data[i+1:], a.data[i:])
	a.data[i] = v
	if len(a.data) > arrayToBitmapThreshold {
		return a.toBitmap()
	}
	return a
}

func (a *arrayContainer) remove(v uint16) container {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	if i < len(a.data) && a.data[i] == v {
		a.data = append(a.data[:i], a.data[i+1:]...)
	}
	return a
}

func (a *arrayContainer) clone() container {
	d := make([]uint16, len(a.data))
	copy(d, a.data)
	return &arrayContainer{data: d}
}

func (a *arrayContainer) toSlice() []uint16 {
	out := make([]uint16, len(a.data))
	copy(out, a.data)
	return out
}

func (a *arrayContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{card: len(a.data)}
	for _, v := range a.data {
		b.words[v>>6] |= 1 << (v & 63)
	}
	return b
}

// ---------------- BitmapContainer ----------------

type bitmapContainer struct {
	words [bitmapWords]uint64
	card  int
}

func (b *bitmapContainer) ctype() byte      { return typeBitmap }
func (b *bitmapContainer) cardinality() int { return b.card }

func (b *bitmapContainer) contains(v uint16) bool {
	return b.words[v>>6]&(1<<(v&63)) != 0
}

func (b *bitmapContainer) add(v uint16) container {
	m := uint64(1) << (v & 63)
	if b.words[v>>6]&m == 0 {
		b.words[v>>6] |= m
		b.card++
	}
	return b
}

func (b *bitmapContainer) remove(v uint16) container {
	m := uint64(1) << (v & 63)
	if b.words[v>>6]&m != 0 {
		b.words[v>>6] &^= m
		b.card--
		if b.card <= arrayToBitmapThreshold {
			return b.toArray()
		}
	}
	return b
}

func (b *bitmapContainer) clone() container {
	c := *b
	return &c
}

func (b *bitmapContainer) toSlice() []uint16 {
	out := make([]uint16, 0, b.card)
	for i, w := range b.words {
		for w != 0 {
			t := bits.TrailingZeros64(w)
			out = append(out, uint16(i<<6+t))
			w &= w - 1
		}
	}
	return out
}

func (b *bitmapContainer) toArray() *arrayContainer {
	return &arrayContainer{data: b.toSlice()}
}

// ---------------- RunContainer (RLE) ----------------

// interval 为闭区间 [start, last]。
type interval struct{ start, last uint16 }

type runContainer struct{ ivs []interval } // 有序且互不重叠、不相邻

func (r *runContainer) ctype() byte { return typeRun }

func (r *runContainer) cardinality() int {
	n := 0
	for _, iv := range r.ivs {
		n += int(iv.last) - int(iv.start) + 1
	}
	return n
}

func (r *runContainer) contains(v uint16) bool {
	i := sort.Search(len(r.ivs), func(i int) bool { return r.ivs[i].last >= v })
	return i < len(r.ivs) && r.ivs[i].start <= v
}

func (r *runContainer) add(v uint16) container {
	i := sort.Search(len(r.ivs), func(i int) bool { return r.ivs[i].last >= v })
	if i < len(r.ivs) && r.ivs[i].start <= v {
		return r // 已存在
	}
	mergeLeft := i > 0 && uint32(r.ivs[i-1].last)+1 == uint32(v)
	mergeRight := i < len(r.ivs) && uint32(v)+1 == uint32(r.ivs[i].start)
	switch {
	case mergeLeft && mergeRight:
		r.ivs[i-1].last = r.ivs[i].last
		r.ivs = append(r.ivs[:i], r.ivs[i+1:]...)
	case mergeLeft:
		r.ivs[i-1].last = v
	case mergeRight:
		r.ivs[i].start = v
	default:
		r.ivs = append(r.ivs, interval{})
		copy(r.ivs[i+1:], r.ivs[i:])
		r.ivs[i] = interval{v, v}
	}
	return r.maybeConvert()
}

func (r *runContainer) remove(v uint16) container {
	i := sort.Search(len(r.ivs), func(i int) bool { return r.ivs[i].last >= v })
	if i == len(r.ivs) || r.ivs[i].start > v {
		return r
	}
	iv := r.ivs[i]
	switch {
	case iv.start == v && iv.last == v:
		r.ivs = append(r.ivs[:i], r.ivs[i+1:]...)
	case iv.start == v:
		r.ivs[i].start = v + 1
	case iv.last == v:
		r.ivs[i].last = v - 1
	default:
		r.ivs = append(r.ivs, interval{})
		copy(r.ivs[i+2:], r.ivs[i+1:])
		r.ivs[i] = interval{iv.start, v - 1}
		r.ivs[i+1] = interval{v + 1, iv.last}
	}
	return r.maybeConvert()
}

// maybeConvert 在 RLE 不再紧凑（4*runs 大于数组或位图开销）时自动降级。
func (r *runContainer) maybeConvert() container {
	card := r.cardinality()
	if card == 0 {
		return &arrayContainer{}
	}
	runBytes := 4 * len(r.ivs)
	if card <= arrayToBitmapThreshold {
		if runBytes <= 2*card {
			return r
		}
		return r.toArray()
	}
	if runBytes <= bitmapBytes {
		return r
	}
	return r.toBitmap()
}

func (r *runContainer) clone() container {
	ivs := make([]interval, len(r.ivs))
	copy(ivs, r.ivs)
	return &runContainer{ivs: ivs}
}

func (r *runContainer) toSlice() []uint16 {
	out := make([]uint16, 0, r.cardinality())
	for _, iv := range r.ivs {
		for v := uint32(iv.start); v <= uint32(iv.last); v++ {
			out = append(out, uint16(v))
		}
	}
	return out
}

func (r *runContainer) toArray() *arrayContainer {
	return &arrayContainer{data: r.toSlice()}
}

func (r *runContainer) toBitmap() *bitmapContainer {
	b := &bitmapContainer{}
	for _, iv := range r.ivs {
		for v := uint32(iv.start); v <= uint32(iv.last); v++ {
			b.words[v>>6] |= 1 << (v & 63)
		}
		b.card += int(iv.last) - int(iv.start) + 1
	}
	return b
}

// ---------------- 位图结果整形 ----------------

// countRuns 统计位图字数组中 1 的连续段数量（词级快路径）。
func countRuns(words *[bitmapWords]uint64) int {
	runs := 0
	prevLast := uint64(0)
	for _, w := range words {
		x := w &^ (w << 1) // 每个上升沿置位
		runs += bits.OnesCount64(x)
		if prevLast == 1 && w&1 == 1 {
			runs--
		}
		prevLast = w >> 63
	}
	return runs
}

// runFromWords 将位图字数组转换为 RLE 容器，调用方保证 runs 较小。
func runFromWords(words *[bitmapWords]uint64, runs int) *runContainer {
	ivs := make([]interval, 0, runs)
	inRun := false
	var start uint32
	for i := 0; i < bitmapWords; i++ {
		w := words[i]
		if !inRun && w == 0 {
			continue
		}
		if inRun && w == ^uint64(0) {
			continue
		}
		base := uint32(i) * 64
		for j := uint32(0); j < 64; j++ {
			set := w&(uint64(1)<<j) != 0
			if set && !inRun {
				inRun = true
				start = base + j
			} else if !set && inRun {
				inRun = false
				ivs = append(ivs, interval{uint16(start), uint16(base + j - 1)})
			}
		}
	}
	if inRun {
		ivs = append(ivs, interval{uint16(start), 0xFFFF})
	}
	return &runContainer{ivs: ivs}
}

// finishBitmap 为位图运算结果选择最优容器：
// 基数 <= 4096 降级为数组；RLE 更紧凑则转为 RunContainer。
func finishBitmap(words *[bitmapWords]uint64, card int) container {
	if card == 0 {
		return &arrayContainer{}
	}
	if card <= arrayToBitmapThreshold {
		b := &bitmapContainer{words: *words, card: card}
		return b.toArray()
	}
	if runs := countRuns(words); 4*runs < bitmapBytes {
		return runFromWords(words, runs)
	}
	return &bitmapContainer{words: *words, card: card}
}
