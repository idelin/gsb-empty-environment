package orgtree

import (
	"reflect"
	"testing"
)

// buildAcceptanceTree 构造验收边界场景：
//   - 知悉 1：录入 E 在业务 [1,10) 汇报给 M1；
//   - 知悉 5：补录 E 在业务 [3,7) 汇报给 M2。
func buildAcceptanceTree(t *testing.T) *Tree {
	tree := New()
	if err := tree.Assign("E", "M1", 1, 10, 1); err != nil {
		t.Fatalf("Assign M1 失败: %v", err)
	}
	if err := tree.Assign("E", "M2", 3, 7, 5); err != nil {
		t.Fatalf("Assign M2 失败: %v", err)
	}
	return tree
}

func managerAt(t *testing.T, tree *Tree, employee string, validTime, sysTime int) string {
	t.Helper()
	manager, ok := tree.ManagerAt(employee, validTime, sysTime)
	if !ok {
		t.Fatalf("业务 %d / 知悉 %d: 未找到 %s 的汇报对象", validTime, sysTime, employee)
	}
	return manager
}

// TestAcceptance 验收边界：
//   - 知悉 6 查询业务 4 -> M2（补录生效）
//   - 知悉 6 查询业务 8 -> M1（补录区间之外仍是原汇报线）
//   - 知悉 4 查询业务 4 -> M1（补录之前系统只知悉 M1）
func TestAcceptance(t *testing.T) {
	tree := buildAcceptanceTree(t)

	if got := managerAt(t, tree, "E", 4, 6); got != "M2" {
		t.Errorf("知悉6/业务4: 期望 M2, 得到 %s", got)
	}
	if got := managerAt(t, tree, "E", 8, 6); got != "M1" {
		t.Errorf("知悉6/业务8: 期望 M1, 得到 %s", got)
	}
	if got := managerAt(t, tree, "E", 4, 4); got != "M1" {
		t.Errorf("知悉4/业务4: 期望 M1, 得到 %s", got)
	}
}

// TestRetroactiveSplit 验证补录导致的任职时间段自动分裂：
// 补录后当前可见记录应为 [1,3)->M1, [3,7)->M2, [7,10)->M1。
func TestRetroactiveSplit(t *testing.T) {
	tree := buildAcceptanceTree(t)

	cases := []struct {
		validTime int
		want      string
	}{
		{1, "M1"}, {2, "M1"},
		{3, "M2"}, {4, "M2"}, {6, "M2"},
		{7, "M1"}, {9, "M1"},
	}
	for _, c := range cases {
		if got := managerAt(t, tree, "E", c.validTime, 6); got != c.want {
			t.Errorf("知悉6/业务%d: 期望 %s, 得到 %s", c.validTime, c.want, got)
		}
	}

	// 边界之外无汇报关系。
	if _, ok := tree.ManagerAt("E", 0, 6); ok {
		t.Error("业务0 不应存在汇报关系")
	}
	if _, ok := tree.ManagerAt("E", 10, 6); ok {
		t.Error("业务10 不应存在汇报关系（右开区间）")
	}
}

// TestHistoryPreserved 验证旧版本记录被关闭而非删除，可完整回溯。
func TestHistoryPreserved(t *testing.T) {
	tree := buildAcceptanceTree(t)

	// 知悉 1 刚录入时，任意业务时刻 [1,10) 都汇报给 M1。
	for _, vt := range []int{1, 4, 9} {
		if got := managerAt(t, tree, "E", vt, 1); got != "M1" {
			t.Errorf("知悉1/业务%d: 期望 M1, 得到 %s", vt, got)
		}
	}

	// 审计视图：原始记录 [1,10)->M1 的 SysTo 应被关闭为 5。
	found := false
	for _, r := range tree.Records() {
		if r.Employee == "E" && r.Manager == "M1" &&
			r.ValidFrom == 1 && r.ValidTo == 10 &&
			r.SysFrom == 1 && r.SysTo == 5 {
			found = true
		}
	}
	if !found {
		t.Error("未找到被关闭的原始记录 [1,10)->M1 (知悉[1,5))")
	}
}

// TestChainAt 验证任意时刻的完整汇报链查询。
func TestChainAt(t *testing.T) {
	tree := New()
	// 知悉 1：E->M1->D->CEO，业务 [1,10)。
	for _, link := range [][2]string{{"E", "M1"}, {"M1", "D"}, {"D", "CEO"}} {
		if err := tree.Assign(link[0], link[1], 1, 10, 1); err != nil {
			t.Fatalf("Assign 失败: %v", err)
		}
	}
	// 知悉 5：补录 E 在业务 [3,7) 汇报给 M2，M2 汇报给 D。
	if err := tree.Assign("E", "M2", 3, 7, 5); err != nil {
		t.Fatalf("Assign 失败: %v", err)
	}
	if err := tree.Assign("M2", "D", 1, 10, 5); err != nil {
		t.Fatalf("Assign 失败: %v", err)
	}

	chain, err := tree.ChainAt("E", 4, 6)
	if err != nil {
		t.Fatalf("ChainAt 失败: %v", err)
	}
	if want := []string{"M2", "D", "CEO"}; !reflect.DeepEqual(chain, want) {
		t.Errorf("知悉6/业务4 汇报链: 期望 %v, 得到 %v", want, chain)
	}

	chain, err = tree.ChainAt("E", 4, 4)
	if err != nil {
		t.Fatalf("ChainAt 失败: %v", err)
	}
	if want := []string{"M1", "D", "CEO"}; !reflect.DeepEqual(chain, want) {
		t.Errorf("知悉4/业务4 汇报链: 期望 %v, 得到 %v", want, chain)
	}

	chain, err = tree.ChainAt("E", 8, 6)
	if err != nil {
		t.Fatalf("ChainAt 失败: %v", err)
	}
	if want := []string{"M1", "D", "CEO"}; !reflect.DeepEqual(chain, want) {
		t.Errorf("知悉6/业务8 汇报链: 期望 %v, 得到 %v", want, chain)
	}
}

// TestChainCycleDetection 验证汇报环检测。
func TestChainCycleDetection(t *testing.T) {
	tree := New()
	if err := tree.Assign("A", "B", 1, 10, 1); err != nil {
		t.Fatalf("Assign 失败: %v", err)
	}
	if err := tree.Assign("B", "A", 1, 10, 1); err != nil {
		t.Fatalf("Assign 失败: %v", err)
	}
	if _, err := tree.ChainAt("A", 5, 5); err == nil {
		t.Error("应检测到汇报环")
	}
}

// TestInvalidInput 验证非法输入。
func TestInvalidInput(t *testing.T) {
	tree := New()
	if err := tree.Assign("E", "M1", 5, 5, 1); err == nil {
		t.Error("空区间应报错")
	}
	if err := tree.Assign("E", "M1", 7, 5, 1); err == nil {
		t.Error("倒置区间应报错")
	}
	if err := tree.Assign("E", "E", 1, 5, 1); err == nil {
		t.Error("自我汇报应报错")
	}
}

// TestMultipleBackfills 验证多次补录可叠加分裂。
func TestMultipleBackfills(t *testing.T) {
	tree := New()
	if err := tree.Assign("E", "M1", 1, 10, 1); err != nil {
		t.Fatalf("Assign 失败: %v", err)
	}
	if err := tree.Assign("E", "M2", 3, 7, 5); err != nil {
		t.Fatalf("Assign 失败: %v", err)
	}
	// 知悉 8 再补录 [2,4)->M3，横跨 M1 与 M2 的区间。
	if err := tree.Assign("E", "M3", 2, 4, 8); err != nil {
		t.Fatalf("Assign 失败: %v", err)
	}

	cases := []struct {
		validTime int
		want      string
	}{
		{1, "M1"}, {2, "M3"}, {3, "M3"}, {4, "M2"}, {6, "M2"}, {7, "M1"}, {9, "M1"},
	}
	for _, c := range cases {
		if got := managerAt(t, tree, "E", c.validTime, 9); got != c.want {
			t.Errorf("知悉9/业务%d: 期望 %s, 得到 %s", c.validTime, c.want, got)
		}
	}
	// 知悉 6（第二次补录之前）仍是第一次补录后的视图。
	if got := managerAt(t, tree, "E", 3, 6); got != "M2" {
		t.Errorf("知悉6/业务3: 期望 M2, 得到 %s", got)
	}
}
