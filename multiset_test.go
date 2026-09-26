package multiset

import (
	"errors"
	"testing"
)

func build(t *testing.T, deltas []Delta) *Multiset {
	t.Helper()
	m := New()
	if err := m.ApplyBatch(deltas); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	return m
}

// 集合 [1,1,3]：第 2 个元素为 1（重复元素按序选择）。
func TestSelectWithDuplicates(t *testing.T) {
	m := build(t, []Delta{{1, 2}, {3, 1}})
	if got := m.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	cases := []struct {
		k    uint64
		want uint64
	}{{1, 1}, {2, 1}, {3, 3}}
	for _, c := range cases {
		got, err := m.Select(c.k)
		if err != nil {
			t.Fatalf("Select(%d): %v", c.k, err)
		}
		if got != c.want {
			t.Errorf("Select(%d) = %d, want %d", c.k, got, c.want)
		}
	}
	if _, err := m.Select(4); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("Select(4) err = %v, want ErrOutOfRange", err)
	}
	if _, err := m.Select(0); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("Select(0) err = %v, want ErrOutOfRange", err)
	}
	if _, err := New().Select(1); !errors.Is(err, ErrEmpty) {
		t.Errorf("Select on empty err = %v, want ErrEmpty", err)
	}
}

// 批量加减：同事务内混合增减，计数不能为负。
func TestBatchAddSub(t *testing.T) {
	m := build(t, []Delta{{1, 3}, {2, 2}, {5, 1}})
	if err := m.ApplyBatch([]Delta{{1, -1}, {2, 2}, {5, -1}, {7, 4}}); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if got := m.Count(1); got != 2 {
		t.Errorf("Count(1) = %d, want 2", got)
	}
	if got := m.Count(2); got != 4 {
		t.Errorf("Count(2) = %d, want 4", got)
	}
	if got := m.Count(5); got != 0 {
		t.Errorf("Count(5) = %d, want 0 (减到 0 应移除)", got)
	}
	if got := m.Count(7); got != 4 {
		t.Errorf("Count(7) = %d, want 4", got)
	}
	if got := m.Len(); got != 10 {
		t.Errorf("Len = %d, want 10", got)
	}
}

// 事务原子性：中途一项导致负数，整批回滚，集合保持原样。
func TestBatchRollbackOnNegative(t *testing.T) {
	m := build(t, []Delta{{1, 2}, {3, 1}})
	before := m.Encode()

	err := m.ApplyBatch([]Delta{{1, 1}, {3, -5}, {9, 2}}) // 3 只有 1 个，减 5 为负
	if !errors.Is(err, ErrNegativeCnt) {
		t.Fatalf("err = %v, want ErrNegativeCnt", err)
	}
	if got := m.Count(1); got != 2 {
		t.Errorf("Count(1) = %d, want 2（应回滚）", got)
	}
	if got := m.Count(9); got != 0 {
		t.Errorf("Count(9) = %d, want 0（应回滚）", got)
	}
	if string(m.Encode()) != string(before) {
		t.Error("回滚后编码与更新前不一致")
	}

	// 对不存在的值做减法同样回滚。
	if err := m.ApplyBatch([]Delta{{1, 1}, {42, -1}}); !errors.Is(err, ErrNegativeCnt) {
		t.Fatalf("err = %v, want ErrNegativeCnt", err)
	}
	if got := m.Count(1); got != 2 {
		t.Errorf("Count(1) = %d, want 2（应回滚）", got)
	}
}

// 减到恰好 0 合法，不算负数。
func TestSubtractToZero(t *testing.T) {
	m := build(t, []Delta{{1, 2}})
	if err := m.ApplyBatch([]Delta{{1, -2}}); err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if m.Len() != 0 || m.Distinct() != 0 {
		t.Errorf("Len=%d Distinct=%d, want 0/0", m.Len(), m.Distinct())
	}
}

// 相交取最小重数。
func TestIntersectMinMultiplicity(t *testing.T) {
	a := build(t, []Delta{{1, 3}, {2, 1}, {4, 2}})
	b := build(t, []Delta{{1, 2}, {2, 5}, {3, 7}})
	inter := a.Intersect(b)
	if got := inter.Count(1); got != 2 {
		t.Errorf("Count(1) = %d, want 2 (min(3,2))", got)
	}
	if got := inter.Count(2); got != 1 {
		t.Errorf("Count(2) = %d, want 1 (min(1,5))", got)
	}
	if got := inter.Count(3); got != 0 {
		t.Errorf("Count(3) = %d, want 0", got)
	}
	if got := inter.Count(4); got != 0 {
		t.Errorf("Count(4) = %d, want 0", got)
	}
	if got := inter.Len(); got != 3 {
		t.Errorf("Len = %d, want 3", got)
	}
	if v, err := inter.Select(3); err != nil || v != 2 {
		t.Errorf("Select(3) = %d, %v; want 2, nil", v, err)
	}
}

// 编码/解码往返一致，且编码不是普通 map 包装：字节数应远小于
// 每个值 8 字节定长 ×3 条数组的朴素布局。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	m := build(t, []Delta{{1, 2}, {3, 1}, {100, 5}, {1000000, 1}})
	data := m.Encode()
	t.Logf("distinct=%d 编码占用 %d 字节", m.Distinct(), len(data))

	back, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if string(back.Encode()) != string(data) {
		t.Error("往返编码不一致")
	}
	if back.Len() != m.Len() || back.Distinct() != m.Distinct() {
		t.Error("往返后规模不一致")
	}
	for _, k := range []uint64{1, 2, 3, 4, 8, 9} {
		got, err := back.Select(k)
		if err != nil {
			t.Fatalf("Select(%d): %v", k, err)
		}
		want, _ := m.Select(k)
		if got != want {
			t.Errorf("Select(%d) = %d, want %d", k, got, want)
		}
	}

	if _, err := Decode([]byte("XXXX")); err == nil {
		t.Error("坏魔数应报错")
	}
	if _, err := Decode(data[:len(data)-1]); err == nil {
		t.Error("截断编码应报错")
	}
}

// 编码布局字节数符合预期：4 字节魔数 + N + 各段 uvarint。
func TestEncodedLayoutSize(t *testing.T) {
	m := build(t, []Delta{{1, 2}, {3, 1}})
	// magic(4) + N(1) + 值delta(1,2 → 2) + 重数(2,1 → 2) + 前缀delta(2,1 → 2) = 11
	if got := m.EncodedSize(); got != 11 {
		t.Errorf("EncodedSize = %d, want 11", got)
	}
}
