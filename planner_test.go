package planner

import (
	"strings"
	"testing"
)

// 验证「新增列 → 填充数据 → 建立约束」即使输入乱序，规划结果也不会错序。
func TestExpandContractOrdering(t *testing.T) {
	ops := ExpandContract("users", "email")
	// 故意乱序：约束在前，加列在后。
	shuffled := []Op{ops[2], ops[1], ops[0]}
	plan, err := Build(shuffled, []string{"table:users"})
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	pos := map[string]int{}
	for _, st := range plan.Steps {
		for _, op := range st.Ops {
			pos[op.Kind] = st.Index
		}
	}
	if !(pos["add_column"] < pos["backfill"] && pos["backfill"] < pos["add_constraint"]) {
		t.Fatalf("顺序错误: add_column=%d backfill=%d add_constraint=%d",
			pos["add_column"], pos["backfill"], pos["add_constraint"])
	}
}

// 直接校验执行序列：错序的序列必须被拒绝。
func TestValidateSequenceRejectsMisorder(t *testing.T) {
	ops := ExpandContract("users", "email")
	// 错序：先建约束再填充。
	bad := []Op{ops[0], ops[2], ops[1]}
	if err := ValidateSequence(bad, []string{"table:users"}); err == nil {
		t.Fatal("错序序列未被拒绝")
	}
	// 正序：必须通过。
	if err := ValidateSequence(ops, []string{"table:users"}); err != nil {
		t.Fatalf("正序序列被误拒: %v", err)
	}
}

// 读写不同对象的操作应合并到同一步骤。
func TestMergeNonConflictingOps(t *testing.T) {
	ops := []Op{
		{ID: "a", Kind: "add_column", Writes: []string{"col:users.a"}, Provides: []string{"col:users.a:exists"}, Rollback: "r"},
		{ID: "b", Kind: "add_column", Writes: []string{"col:orders.b"}, Provides: []string{"col:orders.b:exists"}, Rollback: "r"},
	}
	plan, err := Build(ops, nil)
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("期望合并为 1 步，实际 %d 步", len(plan.Steps))
	}
}

// 写同一对象的两个操作不能合并，必须分步。
func TestConflictingOpsSeparated(t *testing.T) {
	ops := []Op{
		{ID: "a", Kind: "add_column", Writes: []string{"col:users.x"}, Provides: []string{"col:users.x:exists"}, Rollback: "r"},
		{ID: "b", Kind: "backfill", Reads: []string{"col:users.x"}, Writes: []string{"col:users.x"}, Rollback: "r"},
	}
	plan, err := Build(ops, nil)
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("期望 2 步，实际 %d 步", len(plan.Steps))
	}
}

// 缺少回滚语句的操作所在步骤必须标为不可逆边界。
func TestIrreversibleBoundaryMarked(t *testing.T) {
	ops := ExpandContract("users", "email") // backfill 无回滚
	plan, err := Build(ops, []string{"table:users"})
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	if len(plan.IrreversibleBoundaries) != 1 {
		t.Fatalf("期望 1 个不可逆边界，实际 %v", plan.IrreversibleBoundaries)
	}
	found := false
	for _, st := range plan.Steps {
		for _, op := range st.Ops {
			if op.Kind == "backfill" && !st.Irreversible {
				t.Fatal("含不可逆 backfill 的步骤未被标记")
			}
			if op.Kind == "backfill" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("计划中缺少 backfill 操作")
	}
}

// 超过 20 步必须报错。
func TestMaxStepsEnforced(t *testing.T) {
	var ops []Op
	prev := ""
	for i := 0; i < MaxSteps; i++ {
		id := strings.Repeat("x", i+1) // 唯一 id
		op := Op{ID: id, Kind: "step", Writes: []string{"obj:" + id}, Provides: []string{"st:" + id}, Rollback: "r"}
		if prev != "" {
			op.Requires = []string{"st:" + prev}
		}
		ops = append(ops, op)
		prev = id
	}
	if _, err := Build(ops, nil); err != nil {
		t.Fatalf("%d 步应被允许: %v", MaxSteps, err)
	}
	// 再加一步形成 21 步长链。
	ops = append(ops, Op{ID: "extra", Kind: "step", Writes: []string{"obj:extra"},
		Requires: []string{"st:" + prev}, Provides: []string{"st:extra"}, Rollback: "r"})
	if _, err := Build(ops, nil); err == nil {
		t.Fatal("21 步未被拒绝")
	}
}

// 互不依赖的停写操作应被聚拢到同一窗口，最小化停写窗口数。
func TestWriteStopWindowsMinimized(t *testing.T) {
	ops := []Op{
		{ID: "c1", Kind: "add_constraint", Writes: []string{"col:users.a"}, NeedsWriteStop: true, Rollback: "r"},
		{ID: "c2", Kind: "add_constraint", Writes: []string{"col:users.b"}, NeedsWriteStop: true, Rollback: "r"},
		{ID: "n1", Kind: "add_column", Writes: []string{"col:users.c"}, Rollback: "r"},
	}
	plan, err := Build(ops, nil)
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	if plan.WriteStopWindows != 1 {
		t.Fatalf("期望 1 个停写窗口，实际 %d 个", plan.WriteStopWindows)
	}
}

// 有依赖关系的停写操作无法合并，必须各占一个窗口。
func TestDependentWriteStopOpsNotMerged(t *testing.T) {
	ops := []Op{
		{ID: "c1", Kind: "add_constraint", Writes: []string{"col:users.a"},
			Provides: []string{"col:users.a:constrained"}, NeedsWriteStop: true, Rollback: "r"},
		{ID: "c2", Kind: "add_constraint", Writes: []string{"col:users.b"},
			Requires: []string{"col:users.a:constrained"}, NeedsWriteStop: true, Rollback: "r"},
	}
	plan, err := Build(ops, nil)
	if err != nil {
		t.Fatalf("Build 失败: %v", err)
	}
	if plan.WriteStopWindows != 2 {
		t.Fatalf("期望 2 个停写窗口，实际 %d 个", plan.WriteStopWindows)
	}
}

// 前置状态无人提供时必须报错。
func TestMissingPrerequisite(t *testing.T) {
	ops := []Op{
		{ID: "b", Kind: "backfill", Writes: []string{"col:users.x"}, Requires: []string{"col:users.x:exists"}},
	}
	if _, err := Build(ops, nil); err == nil {
		t.Fatal("缺失前置状态未报错")
	}
	// 由初始状态满足时则可通过。
	if _, err := Build(ops, []string{"col:users.x:exists"}); err != nil {
		t.Fatalf("初始状态已满足前置，不应报错: %v", err)
	}
}
