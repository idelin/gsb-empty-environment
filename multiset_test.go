package multiset

import (
	"errors"
	"reflect"
	"testing"
)

// 集合 [1,1,3] 的第 2 个元素是 1（重复元素参与选择）。
func TestSelectWithDuplicates(t *testing.T) {
	m := FromSlice([]int64{1, 1, 3})
	cases := []struct {
		k    uint64
		want int64
	}{
		{1, 1}, {2, 1}, {3, 3},
	}
	for _, c := range cases {
		got, err := m.Select(c.k)
		if err != nil || got != c.want {
			t.Fatalf("Select(%d) = %d, %v; want %d", c.k, got, err, c.want)
		}
	}
	if _, err := m.Select(0); err == nil {
		t.Fatal("Select(0) should fail")
	}
	if _, err := m.Select(4); err == nil {
		t.Fatal("Select(4) beyond total should fail")
	}
}

// 相交取每个值的最小重数。
func TestIntersectMinMultiplicity(t *testing.T) {
	a := FromSlice([]int64{1, 1, 3})
	b := FromSlice([]int64{1, 3, 3, 7})
	got := a.Intersect(b).Elements()
	if want := []int64{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("intersect = %v; want %v", got, want)
	}
	// 输入不被修改。
	if a.Len() != 3 || b.Len() != 4 {
		t.Fatal("intersect must not mutate inputs")
	}
}

// 批量更新原子性：中途一项导致负计数，整批回滚。
func TestApplyAtomicRollback(t *testing.T) {
	m := FromSlice([]int64{1, 1, 3})
	before := m.Elements()
	err := m.Apply(
		Delta{Value: 5, Diff: 2},  // 合法：新增
		Delta{Value: 1, Diff: -1}, // 合法：还剩一个 1
		Delta{Value: 3, Diff: -2}, // 非法：3 只有 1 个
	)
	if !errors.Is(err, ErrNegativeCount) {
		t.Fatalf("err = %v; want ErrNegativeCount", err)
	}
	if got := m.Elements(); !reflect.DeepEqual(got, before) {
		t.Fatalf("state mutated after rollback: %v; want %v", got, before)
	}
	if m.Len() != 3 || m.Count(5) != 0 {
		t.Fatal("rollback left partial changes")
	}
}

// 合法的批量加减一次性提交，前缀和随之更新。
func TestApplyBatchCommit(t *testing.T) {
	m := FromSlice([]int64{1, 1, 3})
	err := m.Apply(
		Delta{Value: 5, Diff: 2},
		Delta{Value: 1, Diff: -1},
		Delta{Value: 3, Diff: -1},
		Delta{Value: 2, Diff: 3},
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 2, 2, 2, 5, 5}; !reflect.DeepEqual(m.Elements(), want) {
		t.Fatalf("elements = %v; want %v", m.Elements(), want)
	}
	// 累计重数（Rank = <=v 的元素个数）。
	for v, want := range map[int64]uint64{0: 0, 1: 1, 2: 4, 4: 4, 5: 6, 99: 6} {
		if got := m.Rank(v); got != want {
			t.Fatalf("Rank(%d) = %d; want %d", v, got, want)
		}
	}
	// 删光一个值后该值从索引中移除。
	if err := m.Apply(Delta{Value: 2, Diff: -3}); err != nil {
		t.Fatal(err)
	}
	if m.Count(2) != 0 || m.Distinct() != 2 {
		t.Fatalf("value 2 not fully removed: distinct=%d", m.Distinct())
	}
}

// 编码/解码往返一致，且 SizeBytes 与实际字节数精确相等。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	m := FromSlice([]int64{-7, 1, 1, 3, 3, 3, 1000})
	enc := m.Encode()
	if got := m.SizeBytes(); got != len(enc) {
		t.Fatalf("SizeBytes = %d; Encode len = %d", got, len(enc))
	}
	back, err := Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Elements(), m.Elements()) {
		t.Fatalf("round trip = %v; want %v", back.Elements(), m.Elements())
	}
	if back.Len() != m.Len() {
		t.Fatal("total mismatch after decode")
	}
}

// 编码确实随数据增长、且远小于朴素 map 的逐元素存储。
func TestEncodingIsCompact(t *testing.T) {
	xs := make([]int64, 0, 10000)
	for i := 0; i < 10000; i++ {
		xs = append(xs, int64(i/100)) // 100 个不同值，每个 100 份
	}
	m := FromSlice(xs)
	if m.Len() != 10000 || m.Distinct() != 100 {
		t.Fatal("setup wrong")
	}
	if size := m.SizeBytes(); size > 1024 {
		t.Fatalf("compressed size %d bytes for 10000 elements; want < 1 KiB", size)
	}
}

// 解码器拒绝损坏数据。
func TestDecodeRejectsCorrupt(t *testing.T) {
	m := FromSlice([]int64{1, 1, 3})
	enc := m.Encode()
	if _, err := Decode(enc[:len(enc)-1]); err == nil {
		t.Fatal("truncated input should fail")
	}
	if _, err := Decode(append(enc, 0x00)); err == nil {
		t.Fatal("trailing bytes should fail")
	}
}
