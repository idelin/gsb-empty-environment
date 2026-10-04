package set32

import "sort"

// kind 是低 16 位容器的类型标签，编码中直接使用这些值。
type kind uint8

const (
	kindArray  kind = 0 // 排序 uint16 数组
	kindBitmap kind = 1 // 固定 8192 字节位图
	kindRuns   kind = 2 // 互不相邻的闭区间 runs
)

// bitmapBytes 是位图容器的固定序列化字节数。
const bitmapBytes = 8192

// 容器序列化字节数（仅载荷，不含块头）。
func arraySize(n int) int { return 2 * n }
func runsSize(rc int) int { return 4 * rc }

// desiredKind 按“实际序列化字节数最小”选择容器；
// 平局时使用固定优先级：array > runs > bitmap。
// n 为元素个数，rc 为 run 个数。
func desiredKind(n, rc int) kind {
	a, r := arraySize(n), runsSize(rc)
	if a <= r && a <= bitmapBytes {
		return kindArray
	}
	if r <= bitmapBytes {
		return kindRuns
	}
	return kindBitmap
}

// container 是低 16 位值的存储容器。所有方法不修改接收者以外的共享状态；
// add/remove 返回（可能新分配的）容器。
type container interface {
	containerKind() kind
	cardinality() int
	contains(v uint16) bool
	add(v uint16) (container, bool)
	remove(v uint16) (container, bool)
	toRuns() []run
	rankLe(v uint16) int // 容器内 <= v 的元素个数
	selectK(k int) (uint16, bool)
	clone() container
}

// convert 把容器内容转换为指定类型。
func convert(c container, want kind) container {
	if c.containerKind() == want {
		return c
	}
	rs := c.toRuns()
	switch want {
	case kindArray:
		return &arrayC{data: arrayFromRuns(rs)}
	case kindBitmap:
		return &bitmapC{words: bitmapFromRuns(rs), n: runsCardinality(rs)}
	default:
		return &runsC{runs: rs}
	}
}

// ---------- arrayC ----------

type arrayC struct {
	data []uint16 // 严格递增
}

func (c *arrayC) containerKind() kind { return kindArray }
func (c *arrayC) cardinality() int    { return len(c.data) }

func (c *arrayC) contains(v uint16) bool {
	i := sort.Search(len(c.data), func(i int) bool { return c.data[i] >= v })
	return i < len(c.data) && c.data[i] == v
}

func (c *arrayC) add(v uint16) (container, bool) {
	i := sort.Search(len(c.data), func(i int) bool { return c.data[i] >= v })
	if i < len(c.data) && c.data[i] == v {
		return c, false
	}
	c.data = append(c.data, 0)
	copy(c.data[i+1:], c.data[i:])
	c.data[i] = v
	return c, true
}

func (c *arrayC) remove(v uint16) (container, bool) {
	i := sort.Search(len(c.data), func(i int) bool { return c.data[i] >= v })
	if i >= len(c.data) || c.data[i] != v {
		return c, false
	}
	copy(c.data[i:], c.data[i+1:])
	c.data = c.data[:len(c.data)-1]
	return c, true
}

func (c *arrayC) toRuns() []run {
	var rs []run
	for _, v := range c.data {
		if n := len(rs); n > 0 && uint32(v) == uint32(rs[n-1].hi)+1 {
			rs[n-1].hi = v
		} else {
			rs = append(rs, run{v, v})
		}
	}
	return rs
}

func (c *arrayC) rankLe(v uint16) int {
	return sort.Search(len(c.data), func(i int) bool { return c.data[i] > v })
}

func (c *arrayC) selectK(k int) (uint16, bool) {
	if k < 0 || k >= len(c.data) {
		return 0, false
	}
	return c.data[k], true
}

func (c *arrayC) clone() container {
	d := make([]uint16, len(c.data))
	copy(d, c.data)
	return &arrayC{data: d}
}

// ---------- bitmapC ----------

type bitmapC struct {
	words [bitmapBytes / 8]uint64
	n     int
}

func (c *bitmapC) containerKind() kind { return kindBitmap }
func (c *bitmapC) cardinality() int    { return c.n }

func (c *bitmapC) contains(v uint16) bool {
	return c.words[v>>6]>>(v&63)&1 == 1
}

func (c *bitmapC) add(v uint16) (container, bool) {
	m := uint64(1) << (v & 63)
	if c.words[v>>6]&m != 0 {
		return c, false
	}
	c.words[v>>6] |= m
	c.n++
	return c, true
}

func (c *bitmapC) remove(v uint16) (container, bool) {
	m := uint64(1) << (v & 63)
	if c.words[v>>6]&m == 0 {
		return c, false
	}
	c.words[v>>6] &^= m
	c.n--
	return c, true
}

func (c *bitmapC) toRuns() []run {
	var rs []run
	open := false
	var cur run
	for wi, w := range c.words {
		for w != 0 {
			tz := trailingZeros64(w)
			idx := wi*64 + tz
			w &= w - 1
			if !open {
				cur = run{uint16(idx), uint16(idx)}
				open = true
			} else if idx == int(cur.hi)+1 {
				cur.hi = uint16(idx)
			} else {
				rs = append(rs, cur)
				cur = run{uint16(idx), uint16(idx)}
			}
		}
	}
	if open {
		rs = append(rs, cur)
	}
	return rs
}

func (c *bitmapC) rankLe(v uint16) int {
	full := int(v) >> 6
	n := 0
	for i := 0; i < full; i++ {
		n += popcount64(c.words[i])
	}
	n += popcount64(c.words[full] & (((uint64(1) << (v & 63)) << 1) - 1))
	return n
}

func (c *bitmapC) selectK(k int) (uint16, bool) {
	if k < 0 || k >= c.n {
		return 0, false
	}
	for wi, w := range c.words {
		pc := popcount64(w)
		if k >= pc {
			k -= pc
			continue
		}
		for w != 0 {
			tz := trailingZeros64(w)
			if k == 0 {
				return uint16(wi*64 + tz), true
			}
			k--
			w &= w - 1
		}
	}
	return 0, false
}

func (c *bitmapC) clone() container {
	nc := &bitmapC{n: c.n}
	nc.words = c.words
	return nc
}

// ---------- runsC ----------

type runsC struct {
	runs []run // 规范：严格递增、互不相邻
}

func (c *runsC) containerKind() kind { return kindRuns }
func (c *runsC) cardinality() int    { return runsCardinality(c.runs) }

func (c *runsC) contains(v uint16) bool {
	i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].hi >= v })
	return i < len(c.runs) && c.runs[i].lo <= v
}

func (c *runsC) add(v uint16) (container, bool) {
	// 找到第一个 hi >= v 的 run；之前的 run 都完整小于 v。
	i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].hi >= v })
	if i < len(c.runs) && c.runs[i].lo <= v {
		return c, false // 已在某个 run 内
	}
	leftAdj := i > 0 && uint32(c.runs[i-1].hi)+1 == uint32(v)
	rightAdj := i < len(c.runs) && uint32(v)+1 == uint32(c.runs[i].lo)
	switch {
	case leftAdj && rightAdj:
		c.runs[i-1].hi = c.runs[i].hi
		copy(c.runs[i:], c.runs[i+1:])
		c.runs = c.runs[:len(c.runs)-1]
	case leftAdj:
		c.runs[i-1].hi = v
	case rightAdj:
		c.runs[i].lo = v
	default:
		c.runs = append(c.runs, run{})
		copy(c.runs[i+1:], c.runs[i:])
		c.runs[i] = run{v, v}
	}
	return c, true
}

func (c *runsC) remove(v uint16) (container, bool) {
	i := sort.Search(len(c.runs), func(i int) bool { return c.runs[i].hi >= v })
	if i >= len(c.runs) || c.runs[i].lo > v {
		return c, false
	}
	r := c.runs[i]
	switch {
	case r.lo == v && r.hi == v:
		copy(c.runs[i:], c.runs[i+1:])
		c.runs = c.runs[:len(c.runs)-1]
	case r.lo == v:
		c.runs[i].lo = v + 1
	case r.hi == v:
		c.runs[i].hi = v - 1
	default:
		// 从中间拆成两段
		c.runs = append(c.runs, run{})
		copy(c.runs[i+2:], c.runs[i+1:])
		c.runs[i] = run{r.lo, v - 1}
		c.runs[i+1] = run{v + 1, r.hi}
	}
	return c, true
}

func (c *runsC) toRuns() []run {
	rs := make([]run, len(c.runs))
	copy(rs, c.runs)
	return rs
}

func (c *runsC) rankLe(v uint16) int {
	n := 0
	for _, r := range c.runs {
		if r.lo > v {
			break
		}
		if r.hi <= v {
			n += runLen(r)
		} else {
			n += int(v) - int(r.lo) + 1
			break
		}
	}
	return n
}

func (c *runsC) selectK(k int) (uint16, bool) {
	if k < 0 {
		return 0, false
	}
	for _, r := range c.runs {
		if k < runLen(r) {
			return r.lo + uint16(k), true
		}
		k -= runLen(r)
	}
	return 0, false
}

func (c *runsC) clone() container {
	rs := make([]run, len(c.runs))
	copy(rs, c.runs)
	return &runsC{runs: rs}
}

// ---------- 转换辅助 ----------

func arrayFromRuns(rs []run) []uint16 {
	out := make([]uint16, 0, runsCardinality(rs))
	for _, r := range rs {
		for v := uint32(r.lo); v <= uint32(r.hi); v++ {
			out = append(out, uint16(v))
		}
	}
	return out
}

func bitmapFromRuns(rs []run) [bitmapBytes / 8]uint64 {
	var w [bitmapBytes / 8]uint64
	for _, r := range rs {
		for v := uint32(r.lo); v <= uint32(r.hi); v++ {
			w[v>>6] |= uint64(1) << (v & 63)
		}
	}
	return w
}

// ---------- 位运算辅助（避免引入 math/bits 的版本差异） ----------

var deBruijn64 = [...]byte{
	0, 1, 48, 2, 57, 49, 28, 3, 61, 58, 50, 42, 38, 29, 17, 4,
	62, 55, 59, 36, 53, 51, 43, 22, 45, 39, 33, 30, 24, 18, 12, 5,
	63, 47, 56, 27, 60, 41, 37, 16, 54, 35, 52, 21, 44, 32, 23, 11,
	46, 26, 40, 15, 34, 20, 31, 10, 25, 14, 19, 9, 13, 8, 7, 6,
}

func trailingZeros64(x uint64) int {
	return int(deBruijn64[(x&-x)*0x03f79d71b4cb0a89>>58])
}

func popcount64(x uint64) int {
	x -= (x >> 1) & 0x5555555555555555
	x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
	x = (x + (x >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((x * 0x0101010101010101) >> 56)
}
