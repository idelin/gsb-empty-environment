package migrationtest

import (
	"testing"

	"prescreen.local/ddd-10554-v3/migration"
	"prescreen.local/ddd-10554-v3/spec"
)

// pendingChip 的初始状态就是“计数已烧录但切换未确认”：
// 影子已暂存代次 1，指针停在 pending，掉电后旧/影子路径都可能被选。
func pendingChip() *spec.Chip {
	return &spec.Chip{
		Owners: []spec.OwnerID{"BL"},
		Positions: []spec.Position{
			{ID: "legacy", Owner: "BL", Scopes: []spec.Scope{"S"}, Public: true},
			{ID: "shadow", Owner: "BL", Scopes: []spec.Scope{"S"}, Shadow: true, Public: true},
		},
		Images: map[string]spec.Image{
			"revoked":  {ID: "revoked", Versions: map[spec.Scope]spec.Generation{"S": 0}},
			"recovery": {ID: "recovery", Versions: map[spec.Scope]spec.Generation{"S": 2}},
		},
		Revoked:  map[spec.OwnerID]string{"BL": "revoked"},
		Recovery: []spec.RecoveryCombo{{Images: map[spec.OwnerID]string{"BL": "recovery"}}},
		Initial: &spec.State{
			Counters: map[spec.PositionID]map[spec.Scope]spec.Generation{
				"legacy": {"S": 1},
				"shadow": {"S": 1},
			},
			// 切换请求已持久化，确认尚未完成。
			Pointers: map[spec.OwnerID]spec.Pointer{"BL": {Phase: spec.PhasePending}},
		},
		MaxBurns: 1,
	}
}

func TestBurnedButSwitchUnconfirmed(t *testing.T) {
	chip := pendingChip()
	planner, _, report := verifyPlanner(t, chip)

	// 起点即 pending，枚举必须包含旧路径与影子路径同时可选的信念，
	// 且这些信念全部通过独立安全核验（撤销映像在两类路径上都被拒绝）。
	if planner.ConcreteStates() < 3 {
		t.Fatalf("具体状态数过少: %d", planner.ConcreteStates())
	}
	for _, fp := range report.UnsafeStates {
		t.Fatalf("pending 窗口出现不安全状态: %s", fp)
	}

	// 影子与旧计数都已到位，剩下只需零烧录的确认动作即可完成。
	if got := planner.WorstCaseBurns(); got != 0 {
		t.Fatalf("pending 起点最坏烧录次数 = %d, 期望 0", got)
	}

	// 端到端：从 pending 起点确认切换，ack 与 boot 两条分支都要安全收敛。
	sess := planner.NewSession()
	action := sess.NextAction()
	if action == nil || (action.Kind != spec.ActionConfirm && action.Kind != spec.ActionRequest) {
		t.Fatalf("期望指针动作（request/confirm），得到 %+v", action)
	}
	if err := sess.Observe(action, &spec.Observation{Kind: spec.ObsAck}); err != nil {
		t.Fatalf("ack 推进失败: %v", err)
	}
	// 若策略先幂等重放 request，则再走一步 confirm，整体仍零烧录。
	for guard := 0; !sess.Done() && guard < 4; guard++ {
		next := sess.NextAction()
		if next == nil {
			t.Fatal("未完成却无动作可执行")
		}
		if err := sess.Observe(next, &spec.Observation{Kind: spec.ObsAck}); err != nil {
			t.Fatalf("指针动作推进失败: %v", err)
		}
	}
	if !sess.Done() {
		t.Fatal("pending 起点经指针动作后应到达终止信念")
	}

	// 重新走 boot 分支：掉电重启后凭公开读回仍应能确认完成。
	sess2 := planner.NewSession()
	act2 := sess2.NextAction()
	model, err := migration.New(chip)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := model.Apply(chip.Initial, act2)
	if err != nil {
		t.Fatalf("确认动作本身非法: %v", err)
	}
	bootObs := model.BootObservation(committed)
	if err := sess2.Observe(act2, bootObs); err != nil {
		t.Fatalf("boot 推进失败: %v", err)
	}
	if !sess2.Done() {
		t.Fatal("boot 后应到达终止信念")
	}
}
