package quota

import (
	"strings"
	"testing"
)

func mustAlloc(t *testing.T, root *Node) *Allocator {
	t.Helper()
	a, err := NewAllocator(root)
	if err != nil {
		t.Fatalf("NewAllocator: %v", err)
	}
	return a
}

func sumAlloc(alloc map[int]int64) int64 {
	var s int64
	for _, v := range alloc {
		s += v
	}
	return s
}

// 平坦树：等权三叶分 10，余数按叶编号轮转。
func TestFlatWeightedMaxMin(t *testing.T) {
	root := &Node{ID: 0, Cap: 10, Children: []*Node{
		{ID: 1, Demand: 100, Weight: 1},
		{ID: 2, Demand: 100, Weight: 1},
		{ID: 3, Demand: 100, Weight: 1},
	}}
	a := mustAlloc(t, root)

	alloc := a.Allocate()
	want := map[int]int64{1: 4, 2: 3, 3: 3} // 余数 1 给最小编号叶 1
	for id, w := range want {
		if alloc[id] != w {
			t.Fatalf("leaf %d: got %d, want %d (alloc=%v)", id, alloc[id], w, alloc)
		}
	}
	if err := a.Verify(alloc); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 游标跨批次保留：连续四批的余数依次落到叶 1、2、3、1。
func TestCursorPersistsAcrossBatches(t *testing.T) {
	root := &Node{ID: 0, Cap: 10, Children: []*Node{
		{ID: 1, Demand: 100, Weight: 1},
		{ID: 2, Demand: 100, Weight: 1},
		{ID: 3, Demand: 100, Weight: 1},
	}}
	a := mustAlloc(t, root)

	wantExtra := []int{1, 2, 3, 1}
	for batch, extra := range wantExtra {
		alloc := a.Allocate()
		for id := 1; id <= 3; id++ {
			want := int64(3)
			if id == extra {
				want = 4
			}
			if alloc[id] != want {
				t.Fatalf("batch %d leaf %d: got %d, want %d", batch, id, alloc[id], want)
			}
		}
		if err := a.Verify(alloc); err != nil {
			t.Fatalf("batch %d Verify: %v", batch, err)
		}
	}
}

// 权重 1:3，容量 8 → 精确 2 与 6。
func TestWeightedShares(t *testing.T) {
	root := &Node{ID: 0, Cap: 8, Children: []*Node{
		{ID: 1, Demand: 100, Weight: 1},
		{ID: 2, Demand: 100, Weight: 3},
	}}
	a := mustAlloc(t, root)
	alloc := a.Allocate()
	if alloc[1] != 2 || alloc[2] != 6 {
		t.Fatalf("got %v, want {1:2 2:6}", alloc)
	}
	if err := a.Verify(alloc); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 需求饱和后剩余额度归其他叶子。
func TestDemandSaturation(t *testing.T) {
	root := &Node{ID: 0, Cap: 100, Children: []*Node{
		{ID: 1, Demand: 5, Weight: 1},
		{ID: 2, Demand: 10, Weight: 1},
	}}
	a := mustAlloc(t, root)
	alloc := a.Allocate()
	if alloc[1] != 5 || alloc[2] != 10 {
		t.Fatalf("got %v, want {1:5 2:10}", alloc)
	}
	if sumAlloc(alloc) != 15 {
		t.Fatalf("sum = %d, want 15", sumAlloc(alloc))
	}
	if err := a.Verify(alloc); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 关键场景：容量 5 平分给两个子树，每个子树份额为 2.5。
// 若逐层取整，每层 floor(2.5)=2，总共只分出 4，丢 1 额度；
// 本实现只在叶处取整，总额必须等于 5。
func TestNoQuotaLostToPerLevelRounding(t *testing.T) {
	root := &Node{ID: 0, Cap: 5, Children: []*Node{
		{ID: 10, Cap: 100, Children: []*Node{{ID: 1, Demand: 100, Weight: 1}}},
		{ID: 11, Cap: 100, Children: []*Node{{ID: 2, Demand: 100, Weight: 1}}},
	}}
	a := mustAlloc(t, root)
	alloc := a.Allocate()
	if got := sumAlloc(alloc); got != 5 {
		t.Fatalf("sum = %d, want 5 (quota lost to rounding): %v", got, alloc)
	}
	// 两叶理想份额均为 2.5，分配必须是 {3,2} 或 {2,3}
	if !((alloc[1] == 3 && alloc[2] == 2) || (alloc[1] == 2 && alloc[2] == 3)) {
		t.Fatalf("unfair split: %v", alloc)
	}
	if err := a.Verify(alloc); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 内部节点 Cap 成为瓶颈：子树内部等分，不越限。
func TestInternalCapIsBottleneck(t *testing.T) {
	root := &Node{ID: 0, Cap: 100, Children: []*Node{
		{ID: 10, Cap: 6, Children: []*Node{
			{ID: 1, Demand: 100, Weight: 1},
			{ID: 2, Demand: 100, Weight: 1},
			{ID: 3, Demand: 100, Weight: 1},
		}},
		{ID: 4, Demand: 100, Weight: 1},
	}}
	a := mustAlloc(t, root)
	alloc := a.Allocate()
	if alloc[1] != 2 || alloc[2] != 2 || alloc[3] != 2 {
		t.Fatalf("subtree split = %v, want 2/2/2", alloc)
	}
	if alloc[4] != 94 {
		t.Fatalf("leaf 4 = %d, want 94", alloc[4])
	}
	if err := a.Verify(alloc); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 节点额度缩小后重新分配：总额随之下降，余数游标继续轮转。
func TestShrinkThenReallocate(t *testing.T) {
	root := &Node{ID: 0, Cap: 10, Children: []*Node{
		{ID: 1, Demand: 100, Weight: 1},
		{ID: 2, Demand: 100, Weight: 1},
		{ID: 3, Demand: 100, Weight: 1},
	}}
	a := mustAlloc(t, root)

	first := a.Allocate()
	if sumAlloc(first) != 10 {
		t.Fatalf("first sum = %d, want 10", first)
	}

	if err := a.SetCap(0, 7); err != nil {
		t.Fatalf("SetCap: %v", err)
	}
	second := a.Allocate()
	if sumAlloc(second) != 7 {
		t.Fatalf("second sum = %d, want 7: %v", sumAlloc(second), second)
	}
	// 7/3 = 2 余 1；游标在第一批发给叶 1 后停在叶 2，故余数给叶 2。
	want := map[int]int64{1: 2, 2: 3, 3: 2}
	for id, w := range want {
		if second[id] != w {
			t.Fatalf("leaf %d: got %d, want %d (alloc=%v)", id, second[id], w, second)
		}
	}
	if err := a.Verify(second); err != nil {
		t.Fatalf("Verify after shrink: %v", err)
	}
}

// 缩小内部节点 Cap，只影响该子树。
func TestShrinkInternalNode(t *testing.T) {
	root := &Node{ID: 0, Cap: 100, Children: []*Node{
		{ID: 10, Cap: 60, Children: []*Node{
			{ID: 1, Demand: 100, Weight: 1},
			{ID: 2, Demand: 100, Weight: 1},
		}},
		{ID: 3, Demand: 100, Weight: 1},
	}}
	a := mustAlloc(t, root)
	if err := a.SetCap(10, 9); err != nil {
		t.Fatalf("SetCap: %v", err)
	}
	alloc := a.Allocate()
	if got := alloc[1] + alloc[2]; got != 9 {
		t.Fatalf("subtree sum = %d, want 9: %v", got, alloc)
	}
	if alloc[3] != 91 {
		t.Fatalf("leaf 3 = %d, want 91", alloc[3])
	}
	if err := a.Verify(alloc); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// 核验器必须拒绝不公平/不可行的分配。
func TestVerifyRejectsBadAllocations(t *testing.T) {
	root := &Node{ID: 0, Cap: 10, Children: []*Node{
		{ID: 1, Demand: 100, Weight: 1},
		{ID: 2, Demand: 100, Weight: 1},
		{ID: 3, Demand: 100, Weight: 1},
	}}
	a := mustAlloc(t, root)

	cases := []struct {
		name  string
		alloc map[int]int64
		want  string
	}{
		{"偏离公平份额", map[int]int64{1: 5, 2: 2, 3: 3}, "deviates"},
		{"总额丢失", map[int]int64{1: 3, 2: 3, 3: 3}, "quota lost"},
		{"缺少叶子", map[int]int64{1: 4, 2: 3}, "missing"},
		{"超过需求", map[int]int64{1: 4, 2: 3, 3: 3}, "ok"}, // 先构造合法再改需求
	}
	for _, tc := range cases[:3] {
		if err := a.Verify(tc.alloc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: Verify err = %v, want substring %q", tc.name, err, tc.want)
		}
	}

	// 超过需求：把叶 1 需求改为 3，则 4 非法。
	if err := a.SetDemand(1, 3); err != nil {
		t.Fatalf("SetDemand: %v", err)
	}
	if err := a.Verify(map[int]int64{1: 4, 2: 3, 3: 3}); err == nil ||
		!strings.Contains(err.Error(), "exceeds demand") {
		t.Fatalf("超需求: Verify err = %v, want 'exceeds demand'", err)
	}
}

// 核验器必须拒绝违反内部节点 Cap 的分配。
func TestVerifyRejectsCapViolation(t *testing.T) {
	root := &Node{ID: 0, Cap: 100, Children: []*Node{
		{ID: 10, Cap: 5, Children: []*Node{
			{ID: 1, Demand: 100, Weight: 1},
			{ID: 2, Demand: 100, Weight: 1},
		}},
		{ID: 3, Demand: 100, Weight: 1},
	}}
	a := mustAlloc(t, root)
	bad := map[int]int64{1: 4, 2: 4, 3: 92} // 子树 8 > cap 5
	if err := a.Verify(bad); err == nil || !strings.Contains(err.Error(), "exceeds cap") {
		t.Fatalf("Verify err = %v, want 'exceeds cap'", err)
	}
}

// 深层树 + 多批游标：余数在不同子树的叶子间按编号轮转。
func TestDeepTreeCursorRotation(t *testing.T) {
	root := &Node{ID: 0, Cap: 7, Children: []*Node{
		{ID: 10, Cap: 50, Children: []*Node{
			{ID: 1, Demand: 100, Weight: 1},
			{ID: 2, Demand: 100, Weight: 1},
		}},
		{ID: 11, Cap: 50, Children: []*Node{
			{ID: 3, Demand: 100, Weight: 1},
			{ID: 4, Demand: 100, Weight: 1},
		}},
	}}
	a := mustAlloc(t, root)
	// 每叶理想份额 7/4 = 1.75，floor 和为 4，余数 3。
	// 第一批余数给叶 1,2,3；第二批游标从叶 4 起，给 4,1,2。
	wantExtra := [][]int{{1, 2, 3}, {4, 1, 2}}
	for batch, extras := range wantExtra {
		alloc := a.Allocate()
		if sumAlloc(alloc) != 7 {
			t.Fatalf("batch %d sum = %d, want 7", batch, sumAlloc(alloc))
		}
		for _, id := range []int{1, 2, 3, 4} {
			want := int64(1)
			for _, e := range extras {
				if e == id {
					want = 2
				}
			}
			if alloc[id] != want {
				t.Fatalf("batch %d leaf %d: got %d, want %d (alloc=%v)",
					batch, id, alloc[id], want, alloc)
			}
		}
		if err := a.Verify(alloc); err != nil {
			t.Fatalf("batch %d Verify: %v", batch, err)
		}
	}
}
