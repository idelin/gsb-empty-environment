package lsmwatch

import (
	"hash/fnv"
	"math"
)

// BloomFilter 是一个基于双重哈希的标准布隆过滤器。
type BloomFilter struct {
	bits []uint64
	m    uint64 // 位数组长度
	k    uint64 // 哈希函数个数
}

// NewBloomFilter 按 expectedKeys 与 bitsPerKey 构造过滤器。
// bitsPerKey 越小误判率越高，用于演示"过滤器需要重建"的场景。
func NewBloomFilter(expectedKeys, bitsPerKey int) *BloomFilter {
	if expectedKeys < 1 {
		expectedKeys = 1
	}
	if bitsPerKey < 1 {
		bitsPerKey = 1
	}
	m := uint64(expectedKeys * bitsPerKey)
	if m < 64 {
		m = 64
	}
	k := uint64(math.Round(0.693 * float64(m) / float64(expectedKeys)))
	if k < 1 {
		k = 1
	}
	if k > 30 {
		k = 30
	}
	return &BloomFilter{bits: make([]uint64, (m+63)/64), m: m, k: k}
}

func hash64(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

// Add 将 key 加入过滤器。
func (f *BloomFilter) Add(key string) {
	h1 := hash64(key)
	h2 := hash64("bloom-salt:"+key) | 1
	for i := uint64(0); i < f.k; i++ {
		loc := (h1 + i*h2) % f.m
		f.bits[loc/64] |= 1 << (loc % 64)
	}
}

// Test 返回 true 表示 key "可能存在"（可能误判），false 表示"一定不存在"。
func (f *BloomFilter) Test(key string) bool {
	h1 := hash64(key)
	h2 := hash64("bloom-salt:"+key) | 1
	for i := uint64(0); i < f.k; i++ {
		loc := (h1 + i*h2) % f.m
		if f.bits[loc/64]&(1<<(loc%64)) == 0 {
			return false
		}
	}
	return true
}
