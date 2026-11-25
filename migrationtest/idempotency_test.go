package migrationtest

import (
	"testing"

	"prescreen.local/ddd-10554-v3/migration"
	"prescreen.local/ddd-10554-v3/spec"
)

func TestRepeatedRecoveryIdempotency(t *testing.T) {
	chip := sharedSplitChip()
	planner, _, report := verifyPlanner(t, chip)
	model, err := migration.New(chip)
	if err != nil {
		t.Fatal(err)
	}

	sess := planner.NewSession()
	// 真实芯片内部状态（宿主不可见，仅测试环境持有）。
	real := chip.Initial
	burnsUsed := 0
	// 模拟最多 30 次“动作 -> 强制掉电重启”的恢复循环。
	for step := 0; step < 30 && !sess.Done(); step++ {
		action := sess.NextAction()
		if action == nil {
			t.Fatalf("step %d 策略未给出动作却未完成", step)
		}

		outcomes, err := model.Outcomes(real, action)
		if err != nil {
			t.Fatalf("step %d 动作非法: %v", step, err)
		}
		var boot *spec.Observation
		var nextReal *spec.State
		for _, oc := range outcomes {
			if oc.Crashed {
				boot = oc.Observation
				nextReal = oc.State
				burnsUsed += oc.Burns
			}
		}
		if boot == nil {
			t.Fatalf("step %d 缺少 boot 分支", step)
		}
		real = nextReal
		if err := sess.Observe(action, boot); err != nil {
			t.Fatalf("step %d 观测推进失败: %v", step, err)
		}
	}

	if !sess.Done() {
		t.Fatal("反复恢复后迁移未收敛")
	}
	if burnsUsed != planner.WorstCaseBurns() {
		t.Fatalf("反复恢复实际烧录 = %d, 期望最坏值 %d", burnsUsed, planner.WorstCaseBurns())
	}
	if !report.OK() {
		t.Fatal("证书核验失败")
	}
}

// TestIdempotentReplay 专门验证“已生效动作重放”的幂等性：
// 对 pending 指针重复 request、对 committed 指针重复 confirm，
// 以及对已暂存影子重复烧录相同代次，都不得产生新的烧录计费或破坏安全性。
func TestIdempotentReplay(t *testing.T) {
	chip := pendingChip()
	model, err := migration.New(chip)
	if err != nil {
		t.Fatal(err)
	}
	state := chip.Initial

	// pending 下重复 request：状态保持 pending，仍是安全状态。
	reReq := &spec.Action{Kind: spec.ActionRequest, Owner: "BL"}
	s2, err := model.Apply(state, reReq)
	if err != nil {
		t.Fatalf("pending 下重放 request 应幂等合法: %v", err)
	}
	if s2.Pointers["BL"].Phase != spec.PhasePending || !model.Safe(s2) {
		t.Fatal("重放 request 改变了阶段或破坏了安全性")
	}

	// 确认后重复 confirm：状态保持 committed。
	confirm := &spec.Action{Kind: spec.ActionConfirm, Owner: "BL"}
	s3, err := model.Apply(state, confirm)
	if err != nil {
		t.Fatalf("confirm 非法: %v", err)
	}
	s4, err := model.Apply(s3, confirm)
	if err != nil {
		t.Fatalf("committed 下重放 confirm 应幂等合法: %v", err)
	}
	if s4.Pointers["BL"].Phase != spec.PhaseCommitted || !model.Safe(s4) {
		t.Fatal("重放 confirm 改变了阶段或破坏了安全性")
	}

	// 对已达代次 1 的影子重复烧相同代次：必须被拒绝为空烧录，不产生计费。
	burnSame := &spec.Action{
		Kind:     spec.ActionBurn,
		Position: "shadow",
		Gen:      map[spec.Scope]spec.Generation{"S": 1},
	}
	if _, err := model.Apply(state, burnSame); err == nil {
		t.Fatal("重复烧录相同代次必须作为空烧录被拒绝，保证不重复计费")
	}
}
