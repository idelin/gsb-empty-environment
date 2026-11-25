package migrationtest

import (
	"testing"

	"prescreen.local/ddd-10554-v3/spec"
)

// observabilityChip 有两个所有者，影子位置全部非公开。
// 当 BL1 已确认切换、BL2 尚未切换时，公开读回只包含两个公开旧位置的
// 代次（指针与非公开影子都不出位），因此“BL1=committed/BL2=stable”
// 与“BL1=pending/BL2=stable”等不同内部状态可对应同一读回。
// 策略必须只凭该读回选择同一个动作，不能依赖不可见的指针阶段。
func observabilityChip() *spec.Chip {
	return &spec.Chip{
		Owners: []spec.OwnerID{"BL1", "BL2"},
		Positions: []spec.Position{
			// 所有位置都非公开：读回不含任何代次，仅能区分“是否重启”，
			// 指针阶段与影子计数完全不可见，迫使多内部状态合并为同一信念。
			{ID: "legacy-1", Owner: "BL1", Scopes: []spec.Scope{"S"}, Public: false},
			{ID: "shadow-1", Owner: "BL1", Scopes: []spec.Scope{"S"}, Shadow: true, Public: false},
			{ID: "legacy-2", Owner: "BL2", Scopes: []spec.Scope{"S"}, Public: false},
			{ID: "shadow-2", Owner: "BL2", Scopes: []spec.Scope{"S"}, Shadow: true, Public: false},
		},
		Images: map[string]spec.Image{
			"revoked":  {ID: "revoked", Versions: map[spec.Scope]spec.Generation{"S": 0}},
			"recovery": {ID: "recovery", Versions: map[spec.Scope]spec.Generation{"S": 2}},
		},
		Revoked: map[spec.OwnerID]string{"BL1": "revoked", "BL2": "revoked"},
		Recovery: []spec.RecoveryCombo{{Images: map[spec.OwnerID]string{
			"BL1": "recovery", "BL2": "recovery",
		}}},
		Initial: &spec.State{
			Counters: map[spec.PositionID]map[spec.Scope]spec.Generation{
				"legacy-1": {"S": 1},
				"shadow-1": {"S": 1},
				"legacy-2": {"S": 1},
				"shadow-2": {"S": 0},
			},
			// BL1 影子已暂存但尚未切换；BL2 影子还没烧。
			Pointers: map[spec.OwnerID]spec.Pointer{
				"BL1": {Phase: spec.PhaseStable},
				"BL2": {Phase: spec.PhaseStable},
			},
		},
		// 第二个上电候选：BL1 已进入 pending（请求持久化、未确认），
		// 但所有位置都非公开，读回与上面的 stable 候选完全相同。
		InitialAlternatives: []*spec.State{{
			Counters: map[spec.PositionID]map[spec.Scope]spec.Generation{
				"legacy-1": {"S": 1},
				"shadow-1": {"S": 1},
				"legacy-2": {"S": 1},
				"shadow-2": {"S": 0},
			},
			Pointers: map[spec.OwnerID]spec.Pointer{
				"BL1": {Phase: spec.PhasePending},
				"BL2": {Phase: spec.PhaseStable},
			},
		}},
		MaxBurns: 2,
	}
}

func TestSameReadbackTwoInternalStates(t *testing.T) {
	chip := observabilityChip()
	planner, cert, report := verifyPlanner(t, chip)

	// 找到至少一个包含多个具体内部状态、但只对应一个公开观测的信念。
	foundAmbiguous := false
	var maxStates int
	for _, bn := range cert.Beliefs {
		if len(bn.States) > 1 {
			foundAmbiguous = true
		}
		if len(bn.States) > maxStates {
			maxStates = len(bn.States)
		}
	}
	if !foundAmbiguous {
		t.Fatalf("枚举未出现多内部状态信念，最大相容状态数=%d", maxStates)
	}

	// 核验器独立确认：策略只以信念（公开观测历史）为键，
	// 同一信念下无论真实内部状态如何，下一步动作唯一。
	if !report.Checks["policy_optimal_and_observational"] {
		t.Fatal("策略未能通过观测一致性独立核验")
	}

	// 非公开影子位置绝不能出现在任何读回里。
	for _, obs := range cert.Observations {
		if obs.Kind != spec.ObsBoot {
			continue
		}
		if len(obs.Readback) != 0 {
			t.Fatalf("全私有芯片的公开读回应为空，实际泄露: %v", obs.Readback)
		}
	}

	// BL2 影子必须烧到代次 1 才能安全切换，最坏烧录 1 次（BL1 已暂存）。
	if got := planner.WorstCaseBurns(); got != 1 {
		t.Fatalf("观测受限场景最坏烧录 = %d, 期望 1", got)
	}
}
