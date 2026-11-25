package migration

import (
	"testing"

	"prescreen.local/ddd-10554-v3/spec"
)

func testChip() *spec.Chip {
	return &spec.Chip{
		Owners: []spec.OwnerID{"BL"},
		Positions: []spec.Position{
			{ID: "legacy", Owner: "BL", Scopes: []spec.Scope{"S"}, Public: true},
			{ID: "shadow", Owner: "BL", Scopes: []spec.Scope{"S"}, Shadow: true, Public: false},
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
				"shadow": {"S": 0},
			},
			Pointers: map[spec.OwnerID]spec.Pointer{"BL": {Phase: spec.PhaseStable}},
		},
		MaxBurns: 1,
	}
}

func TestReadbackHidesNonPublicAndPointers(t *testing.T) {
	m, err := New(testChip())
	if err != nil {
		t.Fatal(err)
	}
	rb := m.Readback(m.chip.Initial)
	if _, ok := rb["shadow"]; ok {
		t.Fatal("非公开影子位置不得出现在读回中")
	}
	if _, ok := rb["legacy"]; !ok {
		t.Fatal("公开旧位置必须出现在读回中")
	}
}

func TestBurnMonotonicAndNoDoubleCharge(t *testing.T) {
	m, err := New(testChip())
	if err != nil {
		t.Fatal(err)
	}
	// 回滚烧录必须被拒绝。
	down := &spec.Action{Kind: spec.ActionBurn, Position: "legacy",
		Gen: map[spec.Scope]spec.Generation{"S": 0}}
	if _, err := m.Apply(m.chip.Initial, down); err == nil {
		t.Fatal("代次下降的烧录必须被拒绝")
	}
	// 相同代次空烧录必须被拒绝，保证重放不重复计费。
	same := &spec.Action{Kind: spec.ActionBurn, Position: "legacy",
		Gen: map[spec.Scope]spec.Generation{"S": 1}}
	if _, err := m.Apply(m.chip.Initial, same); err == nil {
		t.Fatal("无代次升高的空烧录必须被拒绝")
	}
}

func TestSwitchPhaseGuards(t *testing.T) {
	m, err := New(testChip())
	if err != nil {
		t.Fatal(err)
	}
	// stable 下不能直接 confirm。
	if _, err := m.Apply(m.chip.Initial, &spec.Action{Kind: spec.ActionConfirm, Owner: "BL"}); err == nil {
		t.Fatal("未请求切换不能确认")
	}
	// 影子未达撤销代次前 request 必须被安全检查拒绝。
	if _, err := m.Apply(m.chip.Initial, &spec.Action{Kind: spec.ActionRequest, Owner: "BL"}); err == nil {
		t.Fatal("影子未暂存足够代次时进入 pending 必须被拒绝")
	}
}

func TestWorstCaseBurnsOptimal(t *testing.T) {
	p, err := Solve(testChip())
	if err != nil {
		t.Fatal(err)
	}
	// 影子从代次 0 烧到 1 才能安全切换，最优最坏烧录恰好 1 次。
	if p.WorstCaseBurns() != 1 {
		t.Fatalf("最坏烧录 = %d, 期望 1", p.WorstCaseBurns())
	}
}
