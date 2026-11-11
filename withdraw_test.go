package netsec

import (
	"errors"
	"testing"
)

func newWithdrawVerifier(t *testing.T) *Verifier {
	t.Helper()
	trees := newTestTrees(t)
	v, err := New(Config{
		QuasiIDs:     []string{"region", "job"},
		Trees:        trees,
		Levels:       []int{0, 0},
		L:            2,
		MinGroupSize: 2,
	})
	if err != nil {
		t.Fatalf("构建核验器失败: %v", err)
	}
	records := []Record{
		rec("a1", "北京", "工程师", "流感"),
		rec("a2", "北京", "工程师", "糖尿病"),
		rec("a3", "北京", "工程师", "胃病"),
		rec("b1", "上海", "教师", "流感"),
		rec("b2", "上海", "教师", "流感"),
	}
	if err := v.Replace(records); err != nil {
		t.Fatalf("装载记录失败: %v", err)
	}
	if _, err := v.RegroupAndVerify(); err != nil {
		t.Fatalf("首次核验失败: %v", err)
	}
	return v
}

func TestWithdrawFullRegroupAndDiff(t *testing.T) {
	v := newWithdrawVerifier(t)
	beforeRev := v.Revision()

	// b 组（上海/教师）仅含“流感”一种已知值且大小为 2，命中多样性违规。
	result, err := v.Withdraw("b2")
	if err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	if result.Revision != beforeRev+1 {
		t.Fatalf("版本号未递增: before=%d after=%d", beforeRev, result.Revision)
	}
	if v.RecordCount() != 4 {
		t.Fatalf("撤回后记录数应为 4，实际 %d", v.RecordCount())
	}
	if len(result.GroupChanges) != 1 {
		t.Fatalf("仅一个组应受影响，实际 %d", len(result.GroupChanges))
	}
	change := result.GroupChanges[0]
	if change.Kind != GroupShrunk || change.BeforeSize != 2 || change.AfterSize != 1 {
		t.Fatalf("组变化错误: %+v", change)
	}
	if len(change.RemovedMembers) != 1 || change.RemovedMembers[0] != "b2" {
		t.Fatalf("撤回成员证据错误: %v", change.RemovedMembers)
	}

	// 再撤回 b 组最后一条记录：组消失，进入 Resolved，a 组不受影响。
	result, err = v.Withdraw("b1")
	if err != nil {
		t.Fatalf("二次撤回失败: %v", err)
	}
	if len(result.GroupChanges) != 1 || result.GroupChanges[0].Kind != GroupRemoved {
		t.Fatalf("期望组消失，实际 %+v", result.GroupChanges)
	}
	if len(result.Resolved) != 1 || result.Resolved[0].AfterReasons != ReasonNone {
		t.Fatalf("消失组应出现在 Resolved: %+v", result.Resolved)
	}
	if len(result.NewlyViolating) != 0 {
		t.Fatalf("不应出现新违规组: %+v", result.NewlyViolating)
	}
	if !result.After.Compliant() {
		t.Fatalf("剩余 a 组大小为 3 且含 3 种敏感值，应合规: %+v", result.After.Violations)
	}
}

func TestWithdrawCanCreateNewViolation(t *testing.T) {
	v := newWithdrawVerifier(t)
	// a 组原本 3 条 3 种敏感值，撤回一条后只剩 2 种，仍满足 L=2；
	// 再撤回一条后仅剩 1 种，同时大小降到 1，产生新违规。
	if _, err := v.Withdraw("a3"); err != nil {
		t.Fatalf("撤回 a3 失败: %v", err)
	}
	result, err := v.Withdraw("a2")
	if err != nil {
		t.Fatalf("撤回 a2 失败: %v", err)
	}
	if len(result.NewlyViolating) != 1 {
		t.Fatalf("期望 1 个新违规组，实际 %+v", result.NewlyViolating)
	}
	vc := result.NewlyViolating[0]
	if vc.BeforeReasons != ReasonNone || vc.AfterReasons&ReasonDiversity == 0 {
		t.Fatalf("违规翻转证据错误: before=%s after=%s", vc.BeforeReasons, vc.AfterReasons)
	}
	min := result.After.MinimalViolation()
	if min == nil || min.Size != 1 || min.Diversity.DistinctKnown != 1 {
		t.Fatalf("最小违规证据错误: %+v", min)
	}
}

func TestWithdrawErrorsAreAtomic(t *testing.T) {
	v := newWithdrawVerifier(t)
	count := v.RecordCount()

	_, err := v.Withdraw("ghost")
	if !errors.Is(err, ErrUnknownRecord) {
		t.Fatalf("未知 ID 应包装 ErrUnknownRecord，实际 %v", err)
	}
	if v.RecordCount() != count {
		t.Fatal("撤回失败不得改变记录集")
	}

	// 无报告时撤回应明确失败。
	fresh, err := New(Config{
		QuasiIDs: []string{"region", "job"},
		Trees:    newTestTrees(t),
		Levels:   []int{0, 0},
		L:        1,
	})
	if err != nil {
		t.Fatalf("构建核验器失败: %v", err)
	}
	if err := fresh.Replace([]Record{rec("r1", "北京", "工程师", "流感")}); err != nil {
		t.Fatalf("装载失败: %v", err)
	}
	if _, err := fresh.Withdraw("r1"); !errors.Is(err, ErrNoReport) {
		t.Fatalf("无报告撤回应返回 ErrNoReport，实际 %v", err)
	}
	if fresh.RecordCount() != 1 {
		t.Fatal("无报告撤回失败不得改变记录集")
	}
}
