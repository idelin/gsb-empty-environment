package roaring

import (
	"reflect"
	"testing"
)

// 验收边界 1：连续插入 [1,2,3]，应保持 Array 模式。
func TestSparseInsertStaysArray(t *testing.T) {
	b := New()
	for _, v := range []uint32{1, 2, 3} {
		b.Add(v)
	}
	if got := b.ChunkType(0); got != "array" {
		t.Fatalf("chunk type = %q, want array", got)
	}
	if got := b.Cardinality(); got != 3 {
		t.Fatalf("cardinality = %d, want 3", got)
	}
	for _, v := range []uint32{1, 2, 3} {
		if !b.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	if b.Contains(4) {
		t.Fatal("unexpected hit for 4")
	}
}

// 验收边界 2：插入 [10000..15000]（5001 个值 > 4096 阈值），触发 Array→Bitset 升级。
func TestDenseInsertUpgradesToBitset(t *testing.T) {
	b := New()
	b.AddRange(10000, 15000)
	if got := b.ChunkType(0); got != "bitset" {
		t.Fatalf("chunk type = %q, want bitset", got)
	}
	if got := b.Cardinality(); got != 5001 {
		t.Fatalf("cardinality = %d, want 5001", got)
	}
	for _, v := range []uint32{10000, 12500, 15000} {
		if !b.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	for _, v := range []uint32{9999, 15001} {
		if b.Contains(v) {
			t.Fatalf("unexpected hit for %d", v)
		}
	}
}

// 边界：恰好 4096 个元素仍为 Array，第 4097 个触发升级。
func TestUpgradeThresholdBoundary(t *testing.T) {
	b := New()
	b.AddRange(0, arrayMaxSize-1)
	if got := b.ChunkType(0); got != "array" {
		t.Fatalf("at %d elements chunk type = %q, want array", arrayMaxSize, got)
	}
	b.Add(arrayMaxSize)
	if got := b.ChunkType(0); got != "bitset" {
		t.Fatalf("at %d elements chunk type = %q, want bitset", arrayMaxSize+1, got)
	}
}

// 验收边界 3：AND / OR 结果正确，且运算后分块类型自适应收缩。
func TestAndOrAdaptiveShrink(t *testing.T) {
	// a：稠密 chunk0（bitset）+ 稀疏 chunk1（array）
	a := New()
	a.AddRange(10000, 15000)                          // chunk 0 -> bitset
	for _, v := range []uint32{65536, 65538, 65540} { // chunk 1 -> array
		a.Add(v)
	}
	// b：与 a 在 chunk0 上仅有少量重叠，chunk1 完全重叠
	b := New()
	b.AddRange(14999, 16000) // chunk 0 -> array（1002 个）
	for _, v := range []uint32{65536, 65538, 65540} {
		b.Add(v)
	}

	and := And(a, b)
	// chunk0 交集 = {14999, 15000}，基数 2 <= 4096，应收缩为 Array。
	if got := and.ChunkType(0); got != "array" {
		t.Fatalf("AND chunk0 type = %q, want array (shrunk)", got)
	}
	if got := and.ChunkType(1); got != "array" {
		t.Fatalf("AND chunk1 type = %q, want array", got)
	}
	wantAnd := []uint32{14999, 15000, 65536, 65538, 65540}
	if got := and.ToSlice(); !reflect.DeepEqual(got, wantAnd) {
		t.Fatalf("AND = %v, want %v", got, wantAnd)
	}

	or := Or(a, b)
	// chunk0 并集 = [10000,16000]，6001 个，应保持/升级为 Bitset。
	if got := or.ChunkType(0); got != "bitset" {
		t.Fatalf("OR chunk0 type = %q, want bitset", got)
	}
	if got := or.ChunkType(1); got != "array" {
		t.Fatalf("OR chunk1 type = %q, want array", got)
	}
	if got := or.Cardinality(); got != 6001+3 {
		t.Fatalf("OR cardinality = %d, want %d", got, 6004)
	}
	for _, v := range []uint32{10000, 14999, 15000, 16000, 65536, 65540} {
		if !or.Contains(v) {
			t.Fatalf("OR missing %d", v)
		}
	}
}

// 稠密集 AND 稠密集仍稠密时保持 Bitset。
func TestAndDenseStaysBitset(t *testing.T) {
	a, b := New(), New()
	a.AddRange(0, 10000)
	b.AddRange(5000, 15000)
	and := And(a, b)
	if got := and.ChunkType(0); got != "bitset" {
		t.Fatalf("chunk type = %q, want bitset", got)
	}
	if got := and.Cardinality(); got != 5001 {
		t.Fatalf("cardinality = %d, want 5001", got)
	}
}

// 稀疏 Array ∪ 稀疏 Array 超过阈值时升级为 Bitset。
func TestOrSparseUpgradesToBitset(t *testing.T) {
	a, b := New(), New()
	a.AddRange(0, 3000)
	b.AddRange(3001, 6000)
	or := Or(a, b)
	if got := or.ChunkType(0); got != "bitset" {
		t.Fatalf("chunk type = %q, want bitset", got)
	}
	if got := or.Cardinality(); got != 6001 {
		t.Fatalf("cardinality = %d, want 6001", got)
	}
}

// Remove 使 Bitset 基数回落到阈值时收缩为 Array；删空后分块移除。
func TestRemoveShrinksBitset(t *testing.T) {
	b := New()
	b.AddRange(0, arrayMaxSize) // 4097 个 -> bitset
	if got := b.ChunkType(0); got != "bitset" {
		t.Fatalf("chunk type = %q, want bitset", got)
	}
	if !b.Remove(arrayMaxSize) { // 回落到 4096 -> array
		t.Fatal("Remove returned false")
	}
	if got := b.ChunkType(0); got != "array" {
		t.Fatalf("chunk type = %q, want array (shrunk)", got)
	}
	for v := uint32(0); v < arrayMaxSize; v++ {
		b.Remove(v)
	}
	if !b.IsEmpty() || b.ChunkCount() != 0 {
		t.Fatalf("bitmap should be empty, chunks=%d", b.ChunkCount())
	}
}

// 跨多个高 16 位分块的插入与遍历。
func TestMultiChunk(t *testing.T) {
	b := New()
	vals := []uint32{0, 1, 65535, 65536, 70000, 1<<16*3 + 7, 1<<31 + 42}
	for _, v := range vals {
		b.Add(v)
	}
	if got := b.ChunkCount(); got != 4 {
		t.Fatalf("chunk count = %d, want 4", got)
	}
	if got := b.ToSlice(); !reflect.DeepEqual(got, vals) {
		t.Fatalf("ToSlice = %v, want %v", got, vals)
	}
}

// 与朴素 map 实现对拍：随机数据下 AND/OR/Contains 结果一致。
func TestFuzzAgainstNaive(t *testing.T) {
	seed := uint64(42)
	rand := func(n uint32) uint32 { // xorshift，确定性伪随机
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return uint32(seed) % n
	}
	a, b := New(), New()
	na, nb := map[uint32]bool{}, map[uint32]bool{}
	for i := 0; i < 20000; i++ {
		v := rand(1 << 20)
		a.Add(v)
		na[v] = true
		v = rand(1 << 20)
		b.Add(v)
		nb[v] = true
	}
	and, or := And(a, b), Or(a, b)
	for v := uint32(0); v < 1<<20; v += 997 { // 抽样验证
		if and.Contains(v) != (na[v] && nb[v]) {
			t.Fatalf("AND mismatch at %d", v)
		}
		if or.Contains(v) != (na[v] || nb[v]) {
			t.Fatalf("OR mismatch at %d", v)
		}
	}
	wantAndCard := 0
	for v := range na {
		if nb[v] {
			wantAndCard++
		}
	}
	if got := and.Cardinality(); got != wantAndCard {
		t.Fatalf("AND cardinality = %d, want %d", got, wantAndCard)
	}
	if got := or.Cardinality(); got != len(na)+len(nb)-wantAndCard {
		t.Fatalf("OR cardinality = %d, want %d", got, len(na)+len(nb)-wantAndCard)
	}
}

func TestCloneAndEquals(t *testing.T) {
	b := New()
	b.AddRange(0, 5000)
	c := b.Clone()
	if !b.Equals(c) {
		t.Fatal("clone should equal origin")
	}
	c.Remove(0)
	if b.Equals(c) {
		t.Fatal("mutating clone should not affect origin")
	}
	if !b.Contains(0) {
		t.Fatal("origin lost element after clone mutation")
	}
}
