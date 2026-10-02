// Package roaring 实现 Roaring Bitmap 位图压缩索引器。
//
// 32 位整数按高 16 位分块（Chunk），低 16 位在块内存储：
//   - 稀疏时：有序数组（Array Chunk）
//   - 稠密时：4096 位图（Bitset Chunk，1024 个 uint64）
//
// 当 Array Chunk 基数超过 arrayMaxSize 时自动升级为 Bitset Chunk；
// 交集/并集运算后基数回落到阈值以下时自动收缩回 Array Chunk。
package roaring

import "math/bits"

// arrayMaxSize 是 Array Chunk 升级为 Bitset Chunk 的基数阈值（经典 Roaring 取值）。
const arrayMaxSize = 4096

// bitsetWords 是一个 Bitset Chunk 包含的 uint64 字数（1024 * 64 = 65536 位）。
const bitsetWords = 1024

// ChunkType 标识分块的存储类型。
type ChunkType int

const (
	ArrayChunk  ChunkType = iota // 稀疏：有序数组
	BitsetChunk                  // 稠密：位图
)

func (t ChunkType) String() string {
	if t == ArrayChunk {
		return "array"
	}
	return "bitset"
}

// chunk 是单个 16 位块。
type chunk struct {
	typ  ChunkType
	card int
	arr  []uint16            // typ == ArrayChunk 时有效，保持有序
	bits [bitsetWords]uint64 // typ == BitsetChunk 时有效
}

// newArrayChunk 创建空 Array Chunk。
func newArrayChunk() *chunk {
	return &chunk{typ: ArrayChunk}
}

// contains 判断低 16 位值是否存在。
func (c *chunk) contains(v uint16) bool {
	if c.typ == ArrayChunk {
		i := searchUint16(c.arr, v)
		return i < len(c.arr) && c.arr[i] == v
	}
	return c.bits[v>>6]&(uint64(1)<<(v&63)) != 0
}

// add 插入低 16 位值，返回基数是否增加。必要时自动升级为 Bitset Chunk。
func (c *chunk) add(v uint16) bool {
	if c.typ == ArrayChunk {
		i := searchUint16(c.arr, v)
		if i < len(c.arr) && c.arr[i] == v {
			return false
		}
		c.arr = append(c.arr, 0)
		copy(c.arr[i+1:], c.arr[i:])
		c.arr[i] = v
		c.card++
		if c.card > arrayMaxSize {
			c.toBitset()
		}
		return true
	}
	w := v >> 6
	mask := uint64(1) << (v & 63)
	if c.bits[w]&mask != 0 {
		return false
	}
	c.bits[w] |= mask
	c.card++
	return true
}

// toBitset 将 Array Chunk 原地升级为 Bitset Chunk。
func (c *chunk) toBitset() {
	for _, v := range c.arr {
		c.bits[v>>6] |= uint64(1) << (v & 63)
	}
	c.arr = nil
	c.typ = BitsetChunk
}

// toArray 将 Bitset Chunk 原地收缩为 Array Chunk（调用方保证 card <= arrayMaxSize）。
func (c *chunk) toArray() {
	arr := make([]uint16, 0, c.card)
	for w := 0; w < bitsetWords; w++ {
		word := c.bits[w]
		for word != 0 {
			t := word & -word
			arr = append(arr, uint16(w<<6+bits.TrailingZeros64(word)))
			word ^= t
		}
	}
	c.bits = [bitsetWords]uint64{}
	c.arr = arr
	c.typ = ArrayChunk
}

// searchUint16 在有序数组中二分查找 v，返回其位置或应插入位置。
func searchUint16(a []uint16, v uint16) int {
	lo, hi := 0, len(a)
	for lo < hi {
		mid := (lo + hi) >> 1
		if a[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Bitmap 是 Roaring Bitmap，按高 16 位键有序维护分块。
type Bitmap struct {
	keys   []uint16
	chunks []*chunk
	card   int
}

// New 创建空 Bitmap。
func New() *Bitmap {
	return &Bitmap{}
}

// findChunk 返回高 16 位键对应分块的下标；不存在时 ok 为 false，i 为应插入位置。
func (b *Bitmap) findChunk(key uint16) (i int, ok bool) {
	lo, hi := 0, len(b.keys)
	for lo < hi {
		mid := (lo + hi) >> 1
		if b.keys[mid] < key {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(b.keys) && b.keys[lo] == key {
		return lo, true
	}
	return lo, false
}

// Add 插入一个 32 位值。
func (b *Bitmap) Add(x uint32) {
	key, low := uint16(x>>16), uint16(x)
	i, ok := b.findChunk(key)
	if !ok {
		c := newArrayChunk()
		c.add(low)
		b.keys = append(b.keys, 0)
		copy(b.keys[i+1:], b.keys[i:])
		b.keys[i] = key
		b.chunks = append(b.chunks, nil)
		copy(b.chunks[i+1:], b.chunks[i:])
		b.chunks[i] = c
		b.card++
		return
	}
	if b.chunks[i].add(low) {
		b.card++
	}
}

// AddRange 连续插入 [lo, hi] 区间内的所有值（含端点）。
func (b *Bitmap) AddRange(lo, hi uint32) {
	for x := lo; x <= hi; x++ {
		b.Add(x)
	}
}

// Contains 判断值是否存在。
func (b *Bitmap) Contains(x uint32) bool {
	i, ok := b.findChunk(uint16(x >> 16))
	return ok && b.chunks[i].contains(uint16(x))
}

// Cardinality 返回位图中元素总数。
func (b *Bitmap) Cardinality() int {
	return b.card
}

// ChunkTypes 返回各分块（按高 16 位键升序）的键与存储类型，用于观测自适应行为。
func (b *Bitmap) ChunkTypes() []ChunkInfo {
	infos := make([]ChunkInfo, len(b.keys))
	for i := range b.keys {
		infos[i] = ChunkInfo{Key: b.keys[i], Type: b.chunks[i].typ, Cardinality: b.chunks[i].card}
	}
	return infos
}

// ChunkInfo 描述单个分块的统计信息。
type ChunkInfo struct {
	Key         uint16
	Type        ChunkType
	Cardinality int
}

// ToArray 按升序导出全部元素。
func (b *Bitmap) ToArray() []uint32 {
	out := make([]uint32, 0, b.card)
	for i, key := range b.keys {
		base := uint32(key) << 16
		c := b.chunks[i]
		if c.typ == ArrayChunk {
			for _, v := range c.arr {
				out = append(out, base|uint32(v))
			}
		} else {
			for w := 0; w < bitsetWords; w++ {
				word := c.bits[w]
				for word != 0 {
					t := word & -word
					out = append(out, base|uint32(w<<6+bits.TrailingZeros64(word)))
					word ^= t
				}
			}
		}
	}
	return out
}

// chunkAnd 计算两块交集，结果为空时返回 nil。
func chunkAnd(a, b *chunk) *chunk {
	if a.typ == ArrayChunk && b.typ == ArrayChunk {
		out := &chunk{typ: ArrayChunk}
		i, j := 0, 0
		for i < len(a.arr) && j < len(b.arr) {
			switch {
			case a.arr[i] < b.arr[j]:
				i++
			case a.arr[i] > b.arr[j]:
				j++
			default:
				out.arr = append(out.arr, a.arr[i])
				i++
				j++
			}
		}
		out.card = len(out.arr)
		if out.card == 0 {
			return nil
		}
		return out
	}
	// 至少一方为 Bitset：以字为单位做常数级 AND，再按基数自适应收缩。
	var bitsA, bitsB *[bitsetWords]uint64
	if a.typ == BitsetChunk {
		bitsA = &a.bits
	} else {
		bitsA = &[bitsetWords]uint64{}
		copyBitsFromArray(bitsA, a.arr)
	}
	if b.typ == BitsetChunk {
		bitsB = &b.bits
	} else {
		bitsB = &[bitsetWords]uint64{}
		copyBitsFromArray(bitsB, b.arr)
	}
	out := &chunk{typ: BitsetChunk}
	for w := 0; w < bitsetWords; w++ {
		word := bitsA[w] & bitsB[w]
		out.bits[w] = word
		out.card += bits.OnesCount64(word)
	}
	if out.card == 0 {
		return nil
	}
	if out.card <= arrayMaxSize {
		out.toArray()
	}
	return out
}

// copyBitsFromArray 用有序数组填充位图字（临时用于混合类型运算）。
func copyBitsFromArray(dst *[bitsetWords]uint64, arr []uint16) {
	for _, v := range arr {
		dst[v>>6] |= uint64(1) << (v & 63)
	}
}

// chunkOr 计算两块并集。
func chunkOr(a, b *chunk) *chunk {
	if a.typ == ArrayChunk && b.typ == ArrayChunk {
		out := &chunk{typ: ArrayChunk}
		i, j := 0, 0
		for i < len(a.arr) || j < len(b.arr) {
			switch {
			case j >= len(b.arr) || (i < len(a.arr) && a.arr[i] < b.arr[j]):
				out.arr = append(out.arr, a.arr[i])
				i++
			case i >= len(a.arr) || b.arr[j] < a.arr[i]:
				out.arr = append(out.arr, b.arr[j])
				j++
			default:
				out.arr = append(out.arr, a.arr[i])
				i++
				j++
			}
		}
		out.card = len(out.arr)
		if out.card > arrayMaxSize {
			out.toBitset()
		}
		return out
	}
	// 至少一方为 Bitset：以字为单位做常数级 OR。
	var bitsA, bitsB *[bitsetWords]uint64
	if a.typ == BitsetChunk {
		bitsA = &a.bits
	} else {
		bitsA = &[bitsetWords]uint64{}
		copyBitsFromArray(bitsA, a.arr)
	}
	if b.typ == BitsetChunk {
		bitsB = &b.bits
	} else {
		bitsB = &[bitsetWords]uint64{}
		copyBitsFromArray(bitsB, b.arr)
	}
	out := &chunk{typ: BitsetChunk}
	for w := 0; w < bitsetWords; w++ {
		word := bitsA[w] | bitsB[w]
		out.bits[w] = word
		out.card += bits.OnesCount64(word)
	}
	return out
}

// And 计算两个 Bitmap 的交集，返回新 Bitmap；分块类型按结果基数自适应。
func And(a, b *Bitmap) *Bitmap {
	out := New()
	i, j := 0, 0
	for i < len(a.keys) && j < len(b.keys) {
		switch {
		case a.keys[i] < b.keys[j]:
			i++
		case a.keys[i] > b.keys[j]:
			j++
		default:
			if c := chunkAnd(a.chunks[i], b.chunks[j]); c != nil {
				out.keys = append(out.keys, a.keys[i])
				out.chunks = append(out.chunks, c)
				out.card += c.card
			}
			i++
			j++
		}
	}
	return out
}

// Or 计算两个 Bitmap 的并集，返回新 Bitmap；分块类型按结果基数自适应。
func Or(a, b *Bitmap) *Bitmap {
	out := New()
	i, j := 0, 0
	for i < len(a.keys) || j < len(b.keys) {
		switch {
		case j >= len(b.keys) || (i < len(a.keys) && a.keys[i] < b.keys[j]):
			out.keys = append(out.keys, a.keys[i])
			out.chunks = append(out.chunks, a.chunks[i])
			out.card += a.chunks[i].card
			i++
		case i >= len(a.keys) || b.keys[j] < a.keys[i]:
			out.keys = append(out.keys, b.keys[j])
			out.chunks = append(out.chunks, b.chunks[j])
			out.card += b.chunks[j].card
			j++
		default:
			c := chunkOr(a.chunks[i], b.chunks[j])
			out.keys = append(out.keys, a.keys[i])
			out.chunks = append(out.chunks, c)
			out.card += c.card
			i++
			j++
		}
	}
	return out
}
