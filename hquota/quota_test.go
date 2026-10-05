package hquota

import (
	"math"
	"testing"
)

func mustTree(t *testing.T, rootCap int64) *Tree {
	t.Helper()
	tr, err := NewTree("root", rootCap)
	if err != nil {
		t.Fatalf("NewTree: %v", err)
	}
	return tr
}

func mustNode(t *testing.T, tr *Tree, parent, id string, cap int64) {
	t.Helper()
	if err := tr.AddNode(parent, id, cap); err != nil {
		t.Fatalf("AddNode(%s): %v", id, err)
	}
}

func mustLeaf(t *testing.T, tr *Tree, parent string, id int, demand, weight int64) {
	t.Helper()
	if err := tr.AddLeaf(parent, id, demand, weight); err != nil {
		t.Fatalf("AddLeaf(%d): %v", id, err)
	}
}

func sumAlloc(alloc map[int]int64) int64 {
	var s int64
	for _, v := range alloc {
		s += v
	}
	return s
}

// 加权最大最小公平：权重 1:3，根上限 10 -> 精确 2.5 / 7.5。
func TestWeightedMaxMinFlat(t *testing.T) {
	tr := mustTree(t, 10)
	mustLeaf(t, tr, "root", 1, 100, 1)
	mustLeaf(t, tr, "root", 2, 100, 3)

	a := NewAllocator(tr)
	alloc := a.Allocate()

	exact := a.Exact()
	if math.Abs(exact[1]-2.5) > 1e-6 || math.Abs(exact[2]-7.5) > 1e-6 {
		t.Fatalf("exact shares = %v, want 2.5 / 7.5", exact)
	}
	if got := sumAlloc(alloc); got != 10 {
		t.Fatalf("total = %d, want 10 (alloc %v)", got, alloc)
	}
	// 零头 1 单位按轮转给叶 1（游标初始在叶 1）。
	if alloc[1] != 3 || alloc[2] != 7 {
		t.Fatalf("alloc = %v, want {1:3, 2:7}", alloc)
	}
	if v := a.Verify(); len(v) != 0 {
		t.Fatalf("Verify violations: %v", v)
	}
}

// 层级上限：中间节点先触紧，其下叶冻结，其余叶继续注水。
func TestHierarchicalCaps(t *testing.T) {
	tr := mustTree(t, 100)
	mustNode(t, tr, "root", "A", 20)
	mustNode(t, tr, "root", "B", 100)
	mustLeaf(t, tr, "A", 1, 100, 1)
	mustLeaf(t, tr, "A", 2, 100, 1)
	mustLeaf(t, tr, "B", 3, 100, 1)
	mustLeaf(t, tr, "B", 4, 100, 3)

	a := NewAllocator(tr)
	alloc := a.Allocate()

	want := map[int]int64{1: 10, 2: 10, 3: 20, 4: 60}
	for id, w := range want {
		if alloc[id] != w {
			t.Fatalf("alloc[%d] = %d, want %d (full %v)", id, alloc[id], w, alloc)
		}
	}
	if v := a.Verify(); len(v) != 0 {
		t.Fatalf("Verify violations: %v", v)
	}
}

// 需求触紧：叶需求小于公平水位时，剩余额度让给其它叶。
func TestDemandLimited(t *testing.T) {
	tr := mustTree(t, 10)
	mustLeaf(t, tr, "root", 1, 2, 1)
	mustLeaf(t, tr, "root", 2, 100, 1)

	a := NewAllocator(tr)
	alloc := a.Allocate()
	if alloc[1] != 2 || alloc[2] != 8 {
		t.Fatalf("alloc = %v, want {1:2, 2:8}", alloc)
	}
	if v := a.Verify(); len(v) != 0 {
		t.Fatalf("Verify violations: %v", v)
	}
}

// 零头按叶编号轮转，且游标跨批次保留。
func TestRemainderRotationAcrossBatches(t *testing.T) {
	tr := mustTree(t, 4)
	mustLeaf(t, tr, "root", 1, 4, 1)
	mustLeaf(t, tr, "root", 2, 4, 1)
	mustLeaf(t, tr, "root", 3, 4, 1)

	a := NewAllocator(tr)
	// 每批精确份额 4/3，零头 1 单位，游标依次落在叶 1、2、3、1...
	wantSeq := []map[int]int64{
		{1: 2, 2: 1, 3: 1},
		{1: 1, 2: 2, 3: 1},
		{1: 1, 2: 1, 3: 2},
		{1: 2, 2: 1, 3: 1},
	}
	for batch, want := range wantSeq {
		alloc := a.Allocate()
		for id, w := range want {
			if alloc[id] != w {
				t.Fatalf("batch %d: alloc = %v, want %v", batch, alloc, want)
			}
		}
		if v := a.Verify(); len(v) != 0 {
			t.Fatalf("batch %d: Verify violations: %v", batch, v)
		}
	}
}

// 节点额度缩小后重新分配，且仍满足全部约束与公平性。
func TestShrinkAndReallocate(t *testing.T) {
	tr := mustTree(t, 100)
	mustNode(t, tr, "root", "A", 60)
	mustNode(t, tr, "root", "B", 60)
	mustLeaf(t, tr, "A", 1, 100, 1)
	mustLeaf(t, tr, "B", 2, 100, 1)

	a := NewAllocator(tr)
	alloc := a.Allocate()
	if alloc[1] != 50 || alloc[2] != 50 {
		t.Fatalf("initial alloc = %v, want {1:50, 2:50}", alloc)
	}

	// A 缩小到 30 后：叶 1 被压到 30；根仍有 100，但 B 自身上限 60，
	// 故叶 2 为 60 而非 70。
	alloc, err := a.SetCap("A", 30)
	if err != nil {
		t.Fatalf("SetCap: %v", err)
	}
	if alloc[1] != 30 || alloc[2] != 60 {
		t.Fatalf("after shrink alloc = %v, want {1:30, 2:60}", alloc)
	}
	if v := a.Verify(); len(v) != 0 {
		t.Fatalf("Verify violations: %v", v)
	}
}

// 不得先逐层取整丢掉可用额度：根 3，两个中间节点各带一叶，
// 精确份额 1.5/1.5，逐层取整只会分出 1+1=2，本实现必须分满 3。
func TestNoPerLevelRoundingLoss(t *testing.T) {
	tr := mustTree(t, 3)
	mustNode(t, tr, "root", "A", 3)
	mustNode(t, tr, "root", "B", 3)
	mustLeaf(t, tr, "A", 1, 3, 1)
	mustLeaf(t, tr, "B", 2, 3, 1)

	a := NewAllocator(tr)
	alloc := a.Allocate()
	if got := sumAlloc(alloc); got != 3 {
		t.Fatalf("total = %d, want 3 (per-level flooring would give 2); alloc %v", got, alloc)
	}
	if v := a.Verify(); len(v) != 0 {
		t.Fatalf("Verify violations: %v", v)
	}
}

// 深层树 + 多批零头轮转后总量仍等于根上限。
func TestDeepTreeMultiBatch(t *testing.T) {
	tr := mustTree(t, 11)
	mustNode(t, tr, "root", "A", 7)
	mustNode(t, tr, "A", "A1", 4)
	mustNode(t, tr, "root", "B", 9)
	mustLeaf(t, tr, "A1", 1, 10, 1)
	mustLeaf(t, tr, "A1", 2, 10, 2)
	mustLeaf(t, tr, "A", 3, 10, 1)
	mustLeaf(t, tr, "B", 4, 10, 1)
	mustLeaf(t, tr, "B", 5, 10, 1)

	a := NewAllocator(tr)
	for batch := 0; batch < 5; batch++ {
		alloc := a.Allocate()
		if got := sumAlloc(alloc); got != 11 {
			t.Fatalf("batch %d: total = %d, want 11; alloc %v", batch, got, alloc)
		}
		if v := a.Verify(); len(v) != 0 {
			t.Fatalf("batch %d: Verify violations: %v", batch, v)
		}
	}
}

// 核验器必须能发现人为构造的不公平分配。
func TestVerifyDetectsUnfair(t *testing.T) {
	tr := mustTree(t, 10)
	mustLeaf(t, tr, "root", 1, 100, 1)
	mustLeaf(t, tr, "root", 2, 100, 3)

	// 公平基线 {3,7} 或 {2,8}；{6,4} 明显偏向叶 1。
	bad := map[int]int64{1: 6, 2: 4}
	v := tr.VerifyAllocation(bad)
	if len(v) == 0 {
		t.Fatalf("expected violations for unfair allocation %v", bad)
	}

	// 可行性违例：超过节点上限。
	over := map[int]int64{1: 8, 2: 8}
	v = tr.VerifyAllocation(over)
	if len(v) == 0 {
		t.Fatalf("expected violations for infeasible allocation %v", over)
	}

	// 有剩余容量却不满足需求。
	wasted := map[int]int64{1: 2, 2: 2}
	v = tr.VerifyAllocation(wasted)
	if len(v) == 0 {
		t.Fatalf("expected violations for wasteful allocation %v", wasted)
	}
}

// 核验器接受两种合法取整方向。
func TestVerifyAcceptsBothRoundings(t *testing.T) {
	tr := mustTree(t, 10)
	mustLeaf(t, tr, "root", 1, 100, 1)
	mustLeaf(t, tr, "root", 2, 100, 3)

	for _, alloc := range []map[int]int64{{1: 3, 2: 7}, {1: 2, 2: 8}} {
		if v := tr.VerifyAllocation(alloc); len(v) != 0 {
			t.Fatalf("alloc %v should verify, got %v", alloc, v)
		}
	}
}

// 输入校验。
func TestValidation(t *testing.T) {
	if _, err := NewTree("root", -1); err == nil {
		t.Fatal("expected error for negative root cap")
	}
	tr := mustTree(t, 10)
	if err := tr.AddLeaf("root", 1, 5, 0); err == nil {
		t.Fatal("expected error for zero weight")
	}
	if err := tr.AddLeaf("root", 1, -1, 1); err == nil {
		t.Fatal("expected error for negative demand")
	}
	if err := tr.AddLeaf("missing", 2, 1, 1); err == nil {
		t.Fatal("expected error for missing parent")
	}
	if err := tr.AddNode("root", "n", 5); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := tr.AddNode("root", "n", 5); err == nil {
		t.Fatal("expected error for duplicate node")
	}
	if err := tr.SetCap("missing", 1); err == nil {
		t.Fatal("expected error for missing node")
	}
}
