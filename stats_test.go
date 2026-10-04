package statlib

import (
	"errors"
	"math/rand"
	"sort"
	"testing"
)

// reference 用排序数组计算下中位数与两侧绝对偏差和，作为核对基准。
func reference(vals map[int]int64) (median, leftDev, rightDev int64, ok bool) {
	if len(vals) == 0 {
		return 0, 0, 0, false
	}
	a := make([]int64, 0, len(vals))
	for _, v := range vals {
		a = append(a, v)
	}
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
	n := len(a)
	median = a[(n-1)/2]
	low := (n + 1) / 2
	for i := 0; i < low; i++ {
		leftDev += median - a[i]
	}
	for i := low; i < n; i++ {
		rightDev += a[i] - median
	}
	return median, leftDev, rightDev, true
}

func checkAgainstRef(t *testing.T, s *Stats, vals map[int]int64, ctx string) {
	t.Helper()
	m, l, r, ok := s.Query()
	rm, rl, rr, rok := reference(vals)
	if ok != rok {
		t.Fatalf("%s: ok=%v, want %v", ctx, ok, rok)
	}
	if !ok {
		return
	}
	if m != rm || l != rl || r != rr {
		t.Fatalf("%s: got (median=%d left=%d right=%d), want (median=%d left=%d right=%d)",
			ctx, m, l, r, rm, rl, rr)
	}
}

// TestAgainstSortedArray 随机交替增删，每步用排序数组核对。
func TestAgainstSortedArray(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s := New()
	vals := make(map[int]int64)
	nextID := 0

	for step := 0; step < 5000; step++ {
		// 约 55% 添加、45% 删除，制造大量懒删除与重建。
		if len(vals) == 0 || rng.Intn(100) < 55 {
			id := nextID
			nextID++
			v := int64(rng.Intn(2001) - 1000) // 小范围取值，强制大量重复数值
			if err := s.Add(id, v); err != nil {
				t.Fatalf("step %d: Add(%d,%d): %v", step, id, v, err)
			}
			vals[id] = v
		} else {
			ids := make([]int, 0, len(vals))
			for id := range vals {
				ids = append(ids, id)
			}
			id := ids[rng.Intn(len(ids))]
			if err := s.Remove(id); err != nil {
				t.Fatalf("step %d: Remove(%d): %v", step, id, err)
			}
			delete(vals, id)
		}
		checkAgainstRef(t, s, vals, "random ops")
	}
}

// TestAlternatingExtremes 交替增删同一极大/极小值，每步核对。
func TestAlternatingExtremes(t *testing.T) {
	s := New()
	vals := make(map[int]int64)

	// 基础数据。
	for i := 0; i < 60; i++ {
		v := int64((i*37)%50 - 25)
		if err := s.Add(i, v); err != nil {
			t.Fatalf("Add base: %v", err)
		}
		vals[i] = v
	}
	checkAgainstRef(t, s, vals, "base")

	const extremeID = 100000
	extremes := []int64{1 << 40, -(1 << 40)}
	for round := 0; round < 300; round++ {
		v := extremes[round%2]
		if err := s.Add(extremeID, v); err != nil {
			t.Fatalf("round %d: Add extreme: %v", round, err)
		}
		vals[extremeID] = v
		checkAgainstRef(t, s, vals, "after add extreme")

		if err := s.Remove(extremeID); err != nil {
			t.Fatalf("round %d: Remove extreme: %v", round, err)
		}
		delete(vals, extremeID)
		checkAgainstRef(t, s, vals, "after remove extreme")
	}
}

// TestRebuildConsistency 验证清理前后查询结果一致，且自动重建阈值生效。
func TestRebuildConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	s := New()
	vals := make(map[int]int64)

	// 制造大量墓碑：反复增删，积累物理节点。
	nextID := 0
	for i := 0; i < 400; i++ {
		v := int64(rng.Intn(100))
		if err := s.Add(nextID, v); err != nil {
			t.Fatalf("Add: %v", err)
		}
		vals[nextID] = v
		nextID++
	}
	for round := 0; round < 20; round++ {
		// 删掉一批再补回一批，物理节点持续累积。
		ids := make([]int, 0, len(vals))
		for id := range vals {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		removed := make(map[int]int64)
		for _, id := range ids[:len(ids)/2] {
			if err := s.Remove(id); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			removed[id] = vals[id]
			delete(vals, id)
		}
		checkAgainstRef(t, s, vals, "after batch remove")

		// 记录清理前结果，手动强制重建，验证清理前后一致。
		mBefore, lBefore, rBefore, okBefore := s.Query()
		physBefore := s.physical()
		s.rebuild()
		mAfter, lAfter, rAfter, okAfter := s.Query()
		if okBefore != okAfter || mBefore != mAfter || lBefore != lAfter || rBefore != rAfter {
			t.Fatalf("round %d: rebuild changed result: before=(%d,%d,%d,%v) after=(%d,%d,%d,%v)",
				round, mBefore, lBefore, rBefore, okBefore, mAfter, lAfter, rAfter, okAfter)
		}
		if s.physical() != s.Len() {
			t.Fatalf("round %d: after rebuild physical=%d, want %d", round, s.physical(), s.Len())
		}
		t.Logf("round %d: rebuild physical %d -> %d", round, physBefore, s.physical())
		checkAgainstRef(t, s, vals, "after forced rebuild")

		for id, v := range removed {
			if err := s.Add(id, v); err != nil {
				t.Fatalf("re-Add: %v", err)
			}
			vals[id] = v
		}
		checkAgainstRef(t, s, vals, "after re-add")
	}

	// 自动重建：任何时刻物理节点数不应超过 2*有效+32（重建后的紧上界）。
	if s.physical() > 2*s.Len()+rebuildSlack {
		t.Fatalf("physical=%d exceeds 2*%d+%d", s.physical(), s.Len(), rebuildSlack)
	}
}

// TestAutoRebuildTriggered 确认懒删除累积会自动触发重建清理。
func TestAutoRebuildTriggered(t *testing.T) {
	s := New()
	vals := make(map[int]int64)
	// 只留少量有效项，不断增删同一批 ID 制造墓碑。
	for i := 0; i < 10; i++ {
		if err := s.Add(i, int64(i)); err != nil {
			t.Fatalf("Add: %v", err)
		}
		vals[i] = int64(i)
	}
	for round := 0; round < 100; round++ {
		id := 1000 + round
		if err := s.Add(id, int64(round)); err != nil {
			t.Fatalf("Add: %v", err)
		}
		vals[id] = int64(round)
		if err := s.Remove(id); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		delete(vals, id)
		if s.physical() > 2*s.Len()+rebuildSlack {
			t.Fatalf("round %d: physical=%d exceeds threshold (len=%d)", round, s.physical(), s.Len())
		}
		checkAgainstRef(t, s, vals, "auto rebuild")
	}
}

// TestErrors 验证重复添加与删除不存在 ID 的报错。
func TestErrors(t *testing.T) {
	s := New()
	if err := s.Add(1, 10); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Add(1, 20); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate Add: got %v, want ErrDuplicateID", err)
	}
	if err := s.Remove(2); !errors.Is(err, ErrMissingID) {
		t.Fatalf("Remove missing: got %v, want ErrMissingID", err)
	}
	if err := s.Remove(1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s.Remove(1); !errors.Is(err, ErrMissingID) {
		t.Fatalf("Remove twice: got %v, want ErrMissingID", err)
	}
	if _, _, _, ok := s.Query(); ok {
		t.Fatalf("Query on empty: ok=true, want false")
	}
}

// TestSameValueDistinctIDs 相同数值按 ID 区分，可分别删除。
func TestSameValueDistinctIDs(t *testing.T) {
	s := New()
	vals := make(map[int]int64)
	for id := 0; id < 10; id++ {
		if err := s.Add(id, 42); err != nil {
			t.Fatalf("Add: %v", err)
		}
		vals[id] = 42
	}
	checkAgainstRef(t, s, vals, "all same value")

	for id := 0; id < 10; id += 2 {
		if err := s.Remove(id); err != nil {
			t.Fatalf("Remove(%d): %v", id, err)
		}
		delete(vals, id)
		checkAgainstRef(t, s, vals, "remove half")
	}
	m, l, r, ok := s.Query()
	if !ok || m != 42 || l != 0 || r != 0 {
		t.Fatalf("got (%d,%d,%d,%v), want (42,0,0,true)", m, l, r, ok)
	}
}

// TestSingleElement 边界：单元素的增删与查询。
func TestSingleElement(t *testing.T) {
	s := New()
	if err := s.Add(1, -5); err != nil {
		t.Fatalf("Add: %v", err)
	}
	m, l, r, ok := s.Query()
	if !ok || m != -5 || l != 0 || r != 0 {
		t.Fatalf("got (%d,%d,%d,%v), want (-5,0,0,true)", m, l, r, ok)
	}
	if err := s.Remove(1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, _, _, ok := s.Query(); ok {
		t.Fatalf("Query after removing all: ok=true, want false")
	}
}
