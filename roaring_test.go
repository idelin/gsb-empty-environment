package roaring

import (
	"reflect"
	"testing"
)

// 辅助：构造包含指定值的 Bitmap。
func bitmapOf(vals ...uint32) *Bitmap {
	b := New()
	for _, v := range vals {
		b.Add(v)
	}
	return b
}

// 辅助：连续插入 [lo, hi]。
func bitmapOfRange(lo, hi uint32) *Bitmap {
	b := New()
	b.AddRange(lo, hi)
	return b
}

func chunkTypesOf(b *Bitmap) []ChunkType {
	infos := b.ChunkTypes()
	types := make([]ChunkType, len(infos))
	for i, info := range infos {
		types[i] = info.Type
	}
	return types
}

// 验收 1：连续插入 [1, 2, 3]，分块保持 Array 模式。
func TestSparseInsertStaysArrayChunk(t *testing.T) {
	b := bitmapOf(1, 2, 3)
	if got := b.Cardinality(); got != 3 {
		t.Fatalf("cardinality = %d, want 3", got)
	}
	types := chunkTypesOf(b)
	if len(types) != 1 || types[0] != ArrayChunk {
		t.Fatalf("chunk types = %v, want single array chunk", types)
	}
	for _, v := range []uint32{1, 2, 3} {
		if !b.Contains(v) {
			t.Fatalf("expected Contains(%d) to be true", v)
		}
	}
	if b.Contains(4) {
		t.Fatal("expected Contains(4) to be false")
	}
	if got := b.ToArray(); !reflect.DeepEqual(got, []uint32{1, 2, 3}) {
		t.Fatalf("ToArray = %v, want [1 2 3]", got)
	}
}

// 验收 2：插入 [10000..15000]（5001 个值 > 4096 阈值），触发 Array -> Bitset 转换。
func TestDenseInsertTriggersBitsetConversion(t *testing.T) {
	b := New()
	b.AddRange(10000, 10000+4096) // 恰好 4097 个，越过阈值
	types := chunkTypesOf(b)
	if len(types) != 1 || types[0] != BitsetChunk {
		t.Fatalf("chunk types = %v, want single bitset chunk", types)
	}
	b.AddRange(10000+4097, 15000)
	if got := b.Cardinality(); got != 5001 {
		t.Fatalf("cardinality = %d, want 5001", got)
	}
	for _, v := range []uint32{10000, 12345, 15000} {
		if !b.Contains(v) {
			t.Fatalf("expected Contains(%d) to be true", v)
		}
	}
	if b.Contains(9999) || b.Contains(15001) {
		t.Fatal("unexpected membership outside inserted range")
	}
}

// 同一分块内先稀疏后稠密：插入 [1,2,3] 时为 Array，再插入 [10000..15000] 后升级为 Bitset。
func TestChunkUpgradesInPlace(t *testing.T) {
	b := bitmapOf(1, 2, 3)
	if types := chunkTypesOf(b); types[0] != ArrayChunk {
		t.Fatalf("before dense insert: type = %v, want array", types)
	}
	b.AddRange(10000, 15000)
	types := chunkTypesOf(b)
	if len(types) != 1 || types[0] != BitsetChunk {
		t.Fatalf("after dense insert: types = %v, want single bitset chunk", types)
	}
	if got := b.Cardinality(); got != 5004 {
		t.Fatalf("cardinality = %d, want 5004", got)
	}
	for _, v := range []uint32{1, 2, 3, 10000, 15000} {
		if !b.Contains(v) {
			t.Fatalf("expected Contains(%d) to be true", v)
		}
	}
}

// 高 16 位分块：不同 key 的值落入不同分块，且分块按键有序。
func TestHigh16BitChunking(t *testing.T) {
	b := bitmapOf(1, 1<<16, 2<<16|7, 5<<16|100)
	infos := b.ChunkTypes()
	if len(infos) != 4 {
		t.Fatalf("chunk count = %d, want 4", len(infos))
	}
	for i, wantKey := range []uint16{0, 1, 2, 5} {
		if infos[i].Key != wantKey {
			t.Fatalf("chunk[%d].Key = %d, want %d", i, infos[i].Key, wantKey)
		}
		if infos[i].Type != ArrayChunk {
			t.Fatalf("chunk[%d].Type = %v, want array", i, infos[i].Type)
		}
	}
	if got := b.ToArray(); !reflect.DeepEqual(got, []uint32{1, 1 << 16, 2<<16 | 7, 5<<16 | 100}) {
		t.Fatalf("ToArray = %v", got)
	}
}

// 验收 3a：AND —— 稠密 Bitset 交集基数回落到阈值以下，结果分块自适应收缩为 Array。
func TestAndShrinksBitsetToArray(t *testing.T) {
	a := bitmapOf(1, 2, 3)
	a.AddRange(10000, 15000) // chunk0: bitset, 5004 个元素
	b := bitmapOf(2, 3, 4)
	b.AddRange(12000, 16000) // chunk0: bitset, 5003 个元素

	and := And(a, b)
	// 交集 = {2,3} ∪ [12000..15000]，共 2 + 3001 = 3003 个。
	if got := and.Cardinality(); got != 3003 {
		t.Fatalf("AND cardinality = %d, want 3003", got)
	}
	types := chunkTypesOf(and)
	if len(types) != 1 || types[0] != ArrayChunk {
		t.Fatalf("AND chunk types = %v, want bitset result shrink to array", types)
	}
	want := []uint32{2, 3}
	for v := uint32(12000); v <= 15000; v++ {
		want = append(want, v)
	}
	if got := and.ToArray(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AND result mismatch: got %d elements, head=%v", len(got), got[:5])
	}
}

// 验收 3b：AND —— 两个 Array Chunk 走数组合并路径。
func TestAndArrayWithArray(t *testing.T) {
	a := bitmapOf(1, 2, 3, 100, 200)
	b := bitmapOf(2, 3, 4, 200, 300)
	and := And(a, b)
	if got := and.ToArray(); !reflect.DeepEqual(got, []uint32{2, 3, 200}) {
		t.Fatalf("AND = %v, want [2 3 200]", got)
	}
	if types := chunkTypesOf(and); len(types) != 1 || types[0] != ArrayChunk {
		t.Fatalf("AND chunk types = %v, want array", types)
	}
}

// 验收 3c：OR —— 稠密并集保持 Bitset；验证结果内容。
func TestOrKeepsBitsetForDenseUnion(t *testing.T) {
	a := bitmapOf(1, 2, 3)
	a.AddRange(10000, 15000)
	b := bitmapOf(2, 3, 4)
	b.AddRange(12000, 16000)

	or := Or(a, b)
	// 并集 = {1,2,3,4} ∪ [10000..16000]，共 4 + 6001 = 6005 个。
	if got := or.Cardinality(); got != 6005 {
		t.Fatalf("OR cardinality = %d, want 6005", got)
	}
	types := chunkTypesOf(or)
	if len(types) != 1 || types[0] != BitsetChunk {
		t.Fatalf("OR chunk types = %v, want bitset", types)
	}
	for _, v := range []uint32{1, 2, 3, 4, 10000, 13000, 16000} {
		if !or.Contains(v) {
			t.Fatalf("expected OR to contain %d", v)
		}
	}
	for _, v := range []uint32{0, 5, 9999, 16001} {
		if or.Contains(v) {
			t.Fatalf("expected OR not to contain %d", v)
		}
	}
}

// 验收 3d：OR —— 两个 Array Chunk 并集越过阈值，自动升级为 Bitset。
func TestOrUpgradesArrayToBitset(t *testing.T) {
	a := bitmapOfRange(0, 3000)    // 3001 个，array
	b := bitmapOfRange(2000, 6000) // 4001 个，array
	if types := chunkTypesOf(a); types[0] != ArrayChunk {
		t.Fatalf("a type = %v, want array", types)
	}
	or := Or(a, b)
	if got := or.Cardinality(); got != 6001 {
		t.Fatalf("OR cardinality = %d, want 6001", got)
	}
	if types := chunkTypesOf(or); len(types) != 1 || types[0] != BitsetChunk {
		t.Fatalf("OR chunk types = %v, want upgrade to bitset", types)
	}
	for v := uint32(0); v <= 6000; v++ {
		if !or.Contains(v) {
			t.Fatalf("expected OR to contain %d", v)
		}
	}
}

// 混合类型 AND：Bitset ∩ Array。
func TestAndMixedChunkTypes(t *testing.T) {
	a := bitmapOfRange(10000, 15000) // bitset
	b := bitmapOf(9999, 10000, 12345, 15000, 15001)
	and := And(a, b)
	if got := and.ToArray(); !reflect.DeepEqual(got, []uint32{10000, 12345, 15000}) {
		t.Fatalf("AND = %v, want [10000 12345 15000]", got)
	}
	if types := chunkTypesOf(and); len(types) != 1 || types[0] != ArrayChunk {
		t.Fatalf("AND chunk types = %v, want array", types)
	}
}

// 分块不相交：AND 为空，OR 合并两侧分块。
func TestDisjointChunks(t *testing.T) {
	a := bitmapOf(1, 2, 3)            // chunk key 0
	b := bitmapOf(1<<16|10, 1<<16|11) // chunk key 1
	and := And(a, b)
	if and.Cardinality() != 0 || len(and.ChunkTypes()) != 0 {
		t.Fatalf("AND of disjoint = %v, want empty", and.ToArray())
	}
	or := Or(a, b)
	if got := or.ToArray(); !reflect.DeepEqual(got, []uint32{1, 2, 3, 1<<16 | 10, 1<<16 | 11}) {
		t.Fatalf("OR = %v", got)
	}
	infos := or.ChunkTypes()
	if len(infos) != 2 || infos[0].Key != 0 || infos[1].Key != 1 {
		t.Fatalf("OR chunk keys = %+v, want [0 1]", infos)
	}
}

// 重复插入幂等。
func TestDuplicateInsertIsIdempotent(t *testing.T) {
	b := New()
	for i := 0; i < 3; i++ {
		b.Add(42)
	}
	b.AddRange(100, 200)
	b.AddRange(100, 200)
	if got := b.Cardinality(); got != 102 {
		t.Fatalf("cardinality = %d, want 102", got)
	}
}
