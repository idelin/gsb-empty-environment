package bitmap

import (
	"math/bits"
	"sort"
)

const (
	// arrayMaxSize 是 ArrayContainer 的最大基数，超过则转为 BitmapContainer。
	arrayMaxSize = 4096
	// bitmapWords 是 BitmapContainer 的字数（1024 * 64 = 65536 位）。
	bitmapWords = 1024
	// bitmapBytesEst 是 BitmapContainer 的估算字节数。
	bitmapBytesEst = bitmapWords * 8
	// runIntervalBytes 是 RLE 单个区间的估算字节数（start+last 各 2 字节）。
	runIntervalBytes = 4
)

// container 是 16 位低位的值容器。所有实现的遍历必须按升序。
type container interface {
	contains(v uint16) bool
	// add 返回值容器（可能发生类型转换）与是否真正新增。
	add(v uint16) (container, bool)
	cardinality() int
	clone() container
	foreach(fn func(v uint16))
}

// interval 是闭区间 [start, last]。
type interval struct {
	start uint16
	last  uint16
}

func (iv interval) length() int { return int(iv.last) - int(iv.start) + 1 }

// ---------------- ArrayContainer ----------------

type arrayContainer struct {
	values []uint16 // 升序
}

func (ac *arrayContainer) cardinality() int { return len(ac.values) }

func (ac *arrayContainer) contains(v uint16) bool {
	i := sort.Search(len(ac.values), func(i int) bool { return ac.values[i] >= v })
	return i < len(ac.values) && ac.values[i] == v
}

func (ac *arrayContainer) add(v uint16) (container, bool) {
	i := sort.Search(len(ac.values), func(i int) bool { return ac.values[i] >= v })
	if i < len(ac.values) && ac.values[i] == v {
		return ac, false
	}
	ac.values = append(ac.values, 0)
	copy(ac.values[i+1:], ac.values[i:])
	ac.values[i] = v
	if len(ac.values) > arrayMaxSize {
		return toBitmapContainer(ac), true
	}
	return ac, true
}

func (ac *arrayContainer) clone() container {
	return &arrayContainer{values: append([]uint16(nil), ac.values...)}
}

func (ac *arrayContainer) foreach(fn func(v uint16)) {
	for _, v := range ac.values {
		fn(v)
	}
}

// ---------------- BitmapContainer ----------------

type bitmapContainer struct {
	words [bitmapWords]uint64
	card  int
}

func (bc *bitmapContainer) cardinality() int { return bc.card }

func (bc *bitmapContainer) contains(v uint16) bool {
	return bc.words[v>>6]&(uint64(1)<<(v&63)) != 0
}

func (bc *bitmapContainer) add(v uint16) (container, bool) {
	mask := uint64(1) << (v & 63)
	w := &bc.words[v>>6]
	if *w&mask != 0 {
		return bc, false
	}
	*w |= mask
	bc.card++
	return bc, true
}

func (bc *bitmapContainer) clone() container {
	cp := *bc
	return &cp
}

func (bc *bitmapContainer) foreach(fn func(v uint16)) {
	for wi, w := range bc.words {
		for w != 0 {
			b := bits.TrailingZeros64(w)
			fn(uint16(wi<<6 | b))
			w &= w - 1
		}
	}
}

// runCount 统计置位比特的连续段数量。
func (bc *bitmapContainer) runCount() int {
	runs := 0
	var prevTop uint64
	for _, w := range bc.words {
		if w == 0 {
			prevTop = 0
			continue
		}
		// w 中某一位为 1 且前一位（跨字时为上一字的最高位）为 0，则是一个新段的起点。
		shifted := (w << 1) | prevTop
		runs += bits.OnesCount64(w &^ shifted)
		prevTop = w >> 63
	}
	return runs
}

func (bc *bitmapContainer) recomputeCard() {
	card := 0
	for _, w := range bc.words {
		card += bits.OnesCount64(w)
	}
	bc.card = card
}

// bitmapSetRange 将闭区间 [lo, hi] 全部置位。
func bitmapSetRange(bc *bitmapContainer, lo, hi uint16) {
	first, last := lo>>6, hi>>6
	loMask := ^uint64(0) << (lo & 63)
	hiMask := ^uint64(0) >> (63 - (hi & 63))
	if first == last {
		bc.words[first] |= loMask & hiMask
		return
	}
	bc.words[first] |= loMask
	for w := first + 1; w < last; w++ {
		bc.words[w] = ^uint64(0)
	}
	bc.words[last] |= hiMask
}

// bitmapXorRange 将闭区间 [lo, hi] 全部取反。
func bitmapXorRange(bc *bitmapContainer, lo, hi uint16) {
	first, last := lo>>6, hi>>6
	loMask := ^uint64(0) << (lo & 63)
	hiMask := ^uint64(0) >> (63 - (hi & 63))
	if first == last {
		bc.words[first] ^= loMask & hiMask
		return
	}
	bc.words[first] ^= loMask
	for w := first + 1; w < last; w++ {
		bc.words[w] ^= ^uint64(0)
	}
	bc.words[last] ^= hiMask
}

// ---------------- RunContainer（RLE） ----------------

type runContainer struct {
	runs []interval // 升序、互不重叠且互不相邻
}

func (rc *runContainer) cardinality() int {
	card := 0
	for _, iv := range rc.runs {
		card += iv.length()
	}
	return card
}

func (rc *runContainer) contains(v uint16) bool {
	i := sort.Search(len(rc.runs), func(i int) bool { return rc.runs[i].last >= v })
	return i < len(rc.runs) && rc.runs[i].start <= v
}

func (rc *runContainer) add(v uint16) (container, bool) {
	// i 为首个 start > v 的区间下标，因此候选合并区间是 i-1。
	i := sort.Search(len(rc.runs), func(j int) bool { return rc.runs[j].start > v })
	if i > 0 {
		prev := &rc.runs[i-1]
		if v <= prev.last {
			return rc, false // 已包含
		}
		if int(v) == int(prev.last)+1 {
			prev.last = v
			// 延伸后可能与后一个区间相接，需要合并。
			if i < len(rc.runs) && int(rc.runs[i].start) <= int(v)+1 {
				prev.last = rc.runs[i].last
				rc.runs = append(rc.runs[:i], rc.runs[i+1:]...)
			}
			return rc, true
		}
	}
	if i < len(rc.runs) && int(v)+1 == int(rc.runs[i].start) {
		rc.runs[i].start = v
		return rc, true
	}
	rc.runs = append(rc.runs, interval{})
	copy(rc.runs[i+1:], rc.runs[i:])
	rc.runs[i] = interval{start: v, last: v}
	return rc, true
}

func (rc *runContainer) clone() container {
	return &runContainer{runs: append([]interval(nil), rc.runs...)}
}

func (rc *runContainer) foreach(fn func(v uint16)) {
	for _, iv := range rc.runs {
		for v := iv.start; ; v++ {
			fn(v)
			if v == iv.last {
				break
			}
		}
	}
}

// ---------------- 容器转换 ----------------

func toArrayContainer(c container) *arrayContainer {
	if ac, ok := c.(*arrayContainer); ok {
		return ac
	}
	vals := make([]uint16, 0, c.cardinality())
	c.foreach(func(v uint16) { vals = append(vals, v) })
	return &arrayContainer{values: vals}
}

func toBitmapContainer(c container) *bitmapContainer {
	if bc, ok := c.(*bitmapContainer); ok {
		return bc
	}
	bc := &bitmapContainer{}
	c.foreach(func(v uint16) { bc.words[v>>6] |= uint64(1) << (v & 63) })
	bc.card = c.cardinality()
	return bc
}

func toRunContainer(c container) *runContainer {
	if rc, ok := c.(*runContainer); ok {
		return rc
	}
	rc := &runContainer{}
	c.foreach(func(v uint16) {
		if n := len(rc.runs); n > 0 && int(rc.runs[n-1].last)+1 == int(v) {
			rc.runs[n-1].last = v
		} else {
			rc.runs = append(rc.runs, interval{start: v, last: v})
		}
	})
	return rc
}

// optimize 在三种容器间选择估算占用最小的表示。
// 规则：runBytes <= arrayBytes 且 runBytes <= 8192 时选 RLE；
// 否则基数 <= 4096 选 Array，其余选 Bitmap。
func optimize(c container) container {
	card := c.cardinality()
	if card == 0 {
		return &runContainer{}
	}
	var runs int
	switch x := c.(type) {
	case *runContainer:
		runs = len(x.runs)
	case *arrayContainer:
		runs = 1
		for i := 1; i < len(x.values); i++ {
			if int(x.values[i]) != int(x.values[i-1])+1 {
				runs++
			}
		}
	case *bitmapContainer:
		runs = x.runCount()
	}
	runBytes := runs * runIntervalBytes
	arrayBytes := card * 2
	if runBytes <= arrayBytes && runBytes <= bitmapBytesEst {
		return toRunContainer(c)
	}
	if card <= arrayMaxSize {
		return toArrayContainer(c)
	}
	return toBitmapContainer(c)
}

// ---------------- 容器类型元信息 ----------------

const (
	typeArray = iota
	typeBitmap
	typeRun
)

func typeOf(c container) int {
	switch c.(type) {
	case *arrayContainer:
		return typeArray
	case *bitmapContainer:
		return typeBitmap
	default:
		return typeRun
	}
}

func typeName(c container) string {
	switch typeOf(c) {
	case typeArray:
		return "array"
	case typeBitmap:
		return "bitmap"
	default:
		return "run"
	}
}

// containersEqual 按值比较两个容器（与具体表示无关）。
func containersEqual(a, b container) bool {
	if a.cardinality() != b.cardinality() {
		return false
	}
	va := toArrayContainer(a).values
	vb := toArrayContainer(b).values
	for i := range va {
		if va[i] != vb[i] {
			return false
		}
	}
	return true
}
