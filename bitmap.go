// Package roaring 实现 Roaring Bitmap：高 16 位分块、低 16 位
// 在稀疏时用有序数组（Array Chunk）、稠密时用位图（Bitset Chunk）
// 的自适应压缩位图索引。
package roaring

import "sort"

// Bitmap 是分块压缩位图。高 16 位作为 chunk key，低 16 位存入对应 chunk。
// keys 与 chunks 一一对应，keys 始终升序。
type Bitmap struct {
	keys   []uint16
	chunks []chunk
}

// New 创建空位图。
func New() *Bitmap { return &Bitmap{} }

// ChunkType 返回高 16 位为 key 的分块类型："array"、"bitset" 或 ""（不存在）。
// 主要用于观测自适应转换行为。
func (b *Bitmap) ChunkType(key uint16) string {
	i := b.findKey(key)
	if i < 0 {
		return ""
	}
	switch b.chunks[i].(type) {
	case *arrayChunk:
		return "array"
	case *bitsetChunk:
		return "bitset"
	}
	return ""
}

// ChunkCount 返回当前分块数量。
func (b *Bitmap) ChunkCount() int { return len(b.keys) }

// findKey 返回 key 所在下标；不存在时返回 -1。
func (b *Bitmap) findKey(key uint16) int {
	i := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= key })
	if i < len(b.keys) && b.keys[i] == key {
		return i
	}
	return -1
}

// Add 插入一个 32 位值。
func (b *Bitmap) Add(v uint32) {
	key, low := uint16(v>>16), uint16(v)
	i := sort.Search(len(b.keys), func(i int) bool { return b.keys[i] >= key })
	if i < len(b.keys) && b.keys[i] == key {
		b.chunks[i], _ = b.chunks[i].add(low)
		return
	}
	c := newArrayChunk()
	nc, _ := c.add(low)
	b.keys = append(b.keys, 0)
	b.chunks = append(b.chunks, nil)
	copy(b.keys[i+1:], b.keys[i:])
	copy(b.chunks[i+1:], b.chunks[i:])
	b.keys[i], b.chunks[i] = key, nc
}

// AddRange 连续插入 [lo, hi]（含两端）。
func (b *Bitmap) AddRange(lo, hi uint32) {
	for v := lo; ; v++ {
		b.Add(v)
		if v == hi {
			return
		}
	}
}

// Remove 删除一个值；空分块会被移除，过稀疏的 Bitset 自动收缩为 Array。
func (b *Bitmap) Remove(v uint32) bool {
	key, low := uint16(v>>16), uint16(v)
	i := b.findKey(key)
	if i < 0 {
		return false
	}
	nc, changed := b.chunks[i].remove(low)
	if !changed {
		return false
	}
	if nc.cardinality() == 0 {
		b.keys = append(b.keys[:i], b.keys[i+1:]...)
		b.chunks = append(b.chunks[:i], b.chunks[i+1:]...)
		return true
	}
	b.chunks[i] = nc
	return true
}

// Contains 判断值是否存在。
func (b *Bitmap) Contains(v uint32) bool {
	i := b.findKey(uint16(v >> 16))
	return i >= 0 && b.chunks[i].contains(uint16(v))
}

// Cardinality 返回元素总数。
func (b *Bitmap) Cardinality() int {
	n := 0
	for _, c := range b.chunks {
		n += c.cardinality()
	}
	return n
}

// IsEmpty 报告位图是否为空。
func (b *Bitmap) IsEmpty() bool { return len(b.keys) == 0 }

// ToSlice 按升序导出全部元素。
func (b *Bitmap) ToSlice() []uint32 {
	out := make([]uint32, 0, b.Cardinality())
	for i, key := range b.keys {
		hi := uint32(key) << 16
		for _, low := range b.chunks[i].appendTo(nil) {
			out = append(out, hi|uint32(low))
		}
	}
	return out
}

// Equals 判断两个位图内容是否一致。
func (b *Bitmap) Equals(o *Bitmap) bool {
	if len(b.keys) != len(o.keys) {
		return false
	}
	for i := range b.keys {
		if b.keys[i] != o.keys[i] {
			return false
		}
		ba, oa := b.chunks[i].appendTo(nil), o.chunks[i].appendTo(nil)
		if len(ba) != len(oa) {
			return false
		}
		for j := range ba {
			if ba[j] != oa[j] {
				return false
			}
		}
	}
	return true
}

// Clone 深拷贝位图。
func (b *Bitmap) Clone() *Bitmap {
	c := &Bitmap{keys: make([]uint16, len(b.keys)), chunks: make([]chunk, len(b.chunks))}
	copy(c.keys, b.keys)
	for i, ch := range b.chunks {
		c.chunks[i] = ch.clone()
	}
	return c
}

// And 返回 a ∩ b，结果分块类型按基数自适应。
func And(a, b *Bitmap) *Bitmap {
	out := &Bitmap{}
	ia, ib := 0, 0
	for ia < len(a.keys) && ib < len(b.keys) {
		switch {
		case a.keys[ia] < b.keys[ib]:
			ia++
		case a.keys[ia] > b.keys[ib]:
			ib++
		default:
			if c := intersect(a.chunks[ia], b.chunks[ib]); c.cardinality() > 0 {
				out.keys = append(out.keys, a.keys[ia])
				out.chunks = append(out.chunks, c)
			}
			ia++
			ib++
		}
	}
	return out
}

// Or 返回 a ∪ b，结果分块类型按基数自适应。
func Or(a, b *Bitmap) *Bitmap {
	out := &Bitmap{}
	ia, ib := 0, 0
	for ia < len(a.keys) || ib < len(b.keys) {
		switch {
		case ib >= len(b.keys) || (ia < len(a.keys) && a.keys[ia] < b.keys[ib]):
			out.keys = append(out.keys, a.keys[ia])
			out.chunks = append(out.chunks, a.chunks[ia].clone())
			ia++
		case ia >= len(a.keys) || b.keys[ib] < a.keys[ia]:
			out.keys = append(out.keys, b.keys[ib])
			out.chunks = append(out.chunks, b.chunks[ib].clone())
			ib++
		default:
			out.keys = append(out.keys, a.keys[ia])
			out.chunks = append(out.chunks, union(a.chunks[ia], b.chunks[ib]))
			ia++
			ib++
		}
	}
	return out
}

// intersect 按两侧 chunk 类型分派交集实现。
func intersect(x, y chunk) chunk {
	switch xc := x.(type) {
	case *arrayChunk:
		switch yc := y.(type) {
		case *arrayChunk:
			return intersectArrayArray(xc, yc)
		case *bitsetChunk:
			return intersectArrayBitset(xc, yc)
		}
	case *bitsetChunk:
		switch yc := y.(type) {
		case *arrayChunk:
			return intersectArrayBitset(yc, xc)
		case *bitsetChunk:
			return intersectBitsetBitset(xc, yc)
		}
	}
	return nil
}

// intersectArrayArray 双指针归并，O(n+m)。
func intersectArrayArray(x, y *arrayChunk) chunk {
	out := &arrayChunk{data: make([]uint16, 0, min(len(x.data), len(y.data)))}
	i, j := 0, 0
	for i < len(x.data) && j < len(y.data) {
		switch {
		case x.data[i] < y.data[j]:
			i++
		case x.data[i] > y.data[j]:
			j++
		default:
			out.data = append(out.data, x.data[i])
			i++
			j++
		}
	}
	return out
}

// intersectArrayBitset 对数组元素逐一位查询，O(n)。
func intersectArrayBitset(a *arrayChunk, b *bitsetChunk) chunk {
	out := &arrayChunk{data: make([]uint16, 0, len(a.data))}
	for _, v := range a.data {
		if b.contains(v) {
			out.data = append(out.data, v)
		}
	}
	return out
}

// intersectBitsetBitset 按 64 位字并行 AND；基数过低时收缩为 Array。
func intersectBitsetBitset(x, y *bitsetChunk) chunk {
	out := &bitsetChunk{}
	for i := range out.words {
		w := x.words[i] & y.words[i]
		out.words[i] = w
		out.card += popcount(w)
	}
	if out.card <= arrayMaxSize {
		return out.toArray()
	}
	return out
}

// union 按两侧 chunk 类型分派并集实现。
func union(x, y chunk) chunk {
	switch xc := x.(type) {
	case *arrayChunk:
		switch yc := y.(type) {
		case *arrayChunk:
			return unionArrayArray(xc, yc)
		case *bitsetChunk:
			return unionArrayBitset(xc, yc)
		}
	case *bitsetChunk:
		switch yc := y.(type) {
		case *arrayChunk:
			return unionArrayBitset(yc, xc)
		case *bitsetChunk:
			return unionBitsetBitset(xc, yc)
		}
	}
	return nil
}

// unionArrayArray 归并有序数组；超过阈值升级为 Bitset。
func unionArrayArray(x, y *arrayChunk) chunk {
	data := make([]uint16, 0, len(x.data)+len(y.data))
	i, j := 0, 0
	for i < len(x.data) && j < len(y.data) {
		switch {
		case x.data[i] < y.data[j]:
			data = append(data, x.data[i])
			i++
		case x.data[i] > y.data[j]:
			data = append(data, y.data[j])
			j++
		default:
			data = append(data, x.data[i])
			i++
			j++
		}
	}
	data = append(data, x.data[i:]...)
	data = append(data, y.data[j:]...)
	if len(data) > arrayMaxSize {
		return (&arrayChunk{data: data}).toBitset()
	}
	return &arrayChunk{data: data}
}

// unionArrayBitset 以位图为底，逐位置位数组元素。
func unionArrayBitset(a *arrayChunk, b *bitsetChunk) chunk {
	out := b.clone().(*bitsetChunk)
	for _, v := range a.data {
		mask := uint64(1) << (v & 63)
		if out.words[v>>6]&mask == 0 {
			out.words[v>>6] |= mask
			out.card++
		}
	}
	return out
}

// unionBitsetBitset 按 64 位字并行 OR。
func unionBitsetBitset(x, y *bitsetChunk) chunk {
	out := &bitsetChunk{}
	for i := range out.words {
		w := x.words[i] | y.words[i]
		out.words[i] = w
		out.card += popcount(w)
	}
	return out
}

func popcount(w uint64) int {
	// 与 math/bits.OnesCount64 等价的内联实现，避免热点路径函数调用开销。
	w -= (w >> 1) & 0x5555555555555555
	w = (w & 0x3333333333333333) + ((w >> 2) & 0x3333333333333333)
	w = (w + (w >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((w * 0x0101010101010101) >> 56)
}
