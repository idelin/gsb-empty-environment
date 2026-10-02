package lsmguard

import (
	"hash"
	"hash/fnv"
	"math"
)

// BloomFilter 是一个标准布隆过滤器，使用双重哈希生成 k 个哈希函数。
type BloomFilter struct {
	bits []uint64
	m    uint64 // 位数组长度
	k    uint   // 哈希函数个数
	h    hash.Hash64
	// 统计
	queries       uint64 // 总查询次数（MaybePresent 返回 true 的）
	falsePositive uint64 // 其中实际不存在的次数
}

// NewBloomFilter 根据期望元素数量 n 与目标误判率 p 创建过滤器。
func NewBloomFilter(n uint64, p float64) *BloomFilter {
	if n == 0 {
		n = 1
	}
	// m = -n*ln(p) / (ln2)^2
	ln2sq := 0.4804530139182014
	m := uint64(float64(n)*(-logf(p))/ln2sq) + 1
	k := uint(float64(m)/float64(n)*0.6931471805599453) + 1
	return &BloomFilter{
		bits: make([]uint64, (m+63)/64),
		m:    m,
		k:    k,
		h:    fnv.New64a(),
	}
}

func logf(x float64) float64 {
	return math.Log(x)
}

func (b *BloomFilter) hashes(key []byte) (uint64, uint64) {
	b.h.Reset()
	b.h.Write(key)
	h1 := b.h.Sum64()
	// 第二个哈希：加盐再算一次
	b.h.Reset()
	b.h.Write([]byte{0x9e, 0x37, 0x79, 0xb9})
	b.h.Write(key)
	h2 := b.h.Sum64() | 1 // 保证非零
	return h1, h2
}

// Add 将 key 加入过滤器。
func (b *BloomFilter) Add(key []byte) {
	h1, h2 := b.hashes(key)
	for i := uint(0); i < b.k; i++ {
		pos := (h1 + uint64(i)*h2) % b.m
		b.bits[pos/64] |= 1 << (pos % 64)
	}
}

// MaybePresent 返回 true 表示 key 可能存在（可能误判）。
func (b *BloomFilter) MaybePresent(key []byte) bool {
	h1, h2 := b.hashes(key)
	for i := uint(0); i < b.k; i++ {
		pos := (h1 + uint64(i)*h2) % b.m
		if b.bits[pos/64]&(1<<(pos%64)) == 0 {
			return false
		}
	}
	return true
}

// RecordResult 在引擎确认 key 是否真实存在后记录，用于统计误判率。
func (b *BloomFilter) RecordResult(actuallyPresent bool) {
	b.queries++
	if !actuallyPresent {
		b.falsePositive++
	}
}

// FalsePositiveRate 返回观测到的误判率。
func (b *BloomFilter) FalsePositiveRate() float64 {
	if b.queries == 0 {
		return 0
	}
	return float64(b.falsePositive) / float64(b.queries)
}
