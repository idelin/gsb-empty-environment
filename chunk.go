package roaring

import (
	"math/bits"
	"sort"
)

// arrayMaxSize 是 Array Chunk 的最大容量。
// 超过该阈值时升级为 Bitset Chunk；Bitset 基数回落到该阈值及以下时收缩回 Array。
const arrayMaxSize = 4096

// chunk 是低 16 位数据的容器，分 Array（稀疏）与 Bitset（稠密）两种实现。
type chunk interface {
	cardinality() int
	contains(v uint16) bool
	// add 插入元素，必要时返回升级后的新 chunk。
	add(v uint16) (chunk, bool)
	// remove 删除元素，必要时返回收缩后的新 chunk。
	remove(v uint16) (chunk, bool)
	// appendTo 将元素按升序追加到 buf。
	appendTo(buf []uint16) []uint16
	clone() chunk
}

// ---------- Array Chunk：稀疏场景，有序数组 ----------

type arrayChunk struct {
	data []uint16 // 始终有序
}

func newArrayChunk() *arrayChunk {
	return &arrayChunk{data: make([]uint16, 0, 4)}
}

func (a *arrayChunk) cardinality() int { return len(a.data) }

func (a *arrayChunk) contains(v uint16) bool {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	return i < len(a.data) && a.data[i] == v
}

func (a *arrayChunk) add(v uint16) (chunk, bool) {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	if i < len(a.data) && a.data[i] == v {
		return a, false
	}
	a.data = append(a.data, 0)
	copy(a.data[i+1:], a.data[i:])
	a.data[i] = v
	if len(a.data) > arrayMaxSize {
		return a.toBitset(), true
	}
	return a, true
}

func (a *arrayChunk) remove(v uint16) (chunk, bool) {
	i := sort.Search(len(a.data), func(i int) bool { return a.data[i] >= v })
	if i >= len(a.data) || a.data[i] != v {
		return a, false
	}
	a.data = append(a.data[:i], a.data[i+1:]...)
	return a, true
}

func (a *arrayChunk) appendTo(buf []uint16) []uint16 { return append(buf, a.data...) }

func (a *arrayChunk) clone() chunk {
	c := &arrayChunk{data: make([]uint16, len(a.data))}
	copy(c.data, a.data)
	return c
}

func (a *arrayChunk) toBitset() *bitsetChunk {
	b := &bitsetChunk{card: len(a.data)}
	for _, v := range a.data {
		b.words[v>>6] |= 1 << (v & 63)
	}
	return b
}

// ---------- Bitset Chunk：稠密场景，65536 位定长位图 ----------

type bitsetChunk struct {
	words [1024]uint64 // 1024 * 64 = 65536 位
	card  int
}

func (b *bitsetChunk) cardinality() int { return b.card }

func (b *bitsetChunk) contains(v uint16) bool {
	return b.words[v>>6]&(1<<(v&63)) != 0
}

func (b *bitsetChunk) add(v uint16) (chunk, bool) {
	mask := uint64(1) << (v & 63)
	if b.words[v>>6]&mask != 0 {
		return b, false
	}
	b.words[v>>6] |= mask
	b.card++
	return b, true
}

func (b *bitsetChunk) remove(v uint16) (chunk, bool) {
	mask := uint64(1) << (v & 63)
	if b.words[v>>6]&mask == 0 {
		return b, false
	}
	b.words[v>>6] &^= mask
	b.card--
	if b.card <= arrayMaxSize {
		return b.toArray(), true
	}
	return b, true
}

func (b *bitsetChunk) appendTo(buf []uint16) []uint16 {
	for i, w := range b.words {
		for w != 0 {
			t := w & -w
			buf = append(buf, uint16(i<<6+bits.TrailingZeros64(t)))
			w ^= t
		}
	}
	return buf
}

func (b *bitsetChunk) clone() chunk {
	c := &bitsetChunk{words: b.words, card: b.card}
	return c
}

func (b *bitsetChunk) toArray() *arrayChunk {
	a := &arrayChunk{data: make([]uint16, 0, b.card)}
	a.data = b.appendTo(a.data)
	return a
}
