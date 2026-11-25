// Package migrationtest 对迁移规划器与独立核验器做小芯片完整枚举测试。
package migrationtest

import (
	"testing"

	"prescreen.local/ddd-10554-v3/migration"
	"prescreen.local/ddd-10554-v3/spec"
	"prescreen.local/ddd-10554-v3/verifier"
)

func owner(o string) spec.OwnerID  { return spec.OwnerID(o) }
func pos(p string) spec.PositionID { return spec.PositionID(p) }
func sc(s string) spec.Scope       { return spec.Scope(s) }

func counter(m map[spec.Scope]spec.Generation) map[spec.Scope]spec.Generation { return m }

// verifyPlanner 是所有用例共用的“求解 -> 出证书 -> 独立核验”闭环。
func verifyPlanner(t *testing.T, chip *spec.Chip) (*migration.Planner, *spec.Certificate, *verifier.Report) {
	t.Helper()
	planner, err := migration.Solve(chip)
	if err != nil {
		t.Fatalf("规划失败: %v", err)
	}
	cert, err := planner.BuildCertificate()
	if err != nil {
		t.Fatalf("证书生成失败: %v", err)
	}
	report := verifier.Verify(cert)
	if !report.OK() {
		t.Fatalf("独立核验失败: %v; 检查项=%v", report.Errors, report.Checks)
	}
	if report.WorstCaseBurns != planner.WorstCaseBurns() {
		t.Fatalf("最坏烧录次数不一致: planner=%d verifier=%d",
			planner.WorstCaseBurns(), report.WorstCaseBurns)
	}
	return planner, cert, report
}

// sharedSplitChip 覆盖“共享位置分裂”：所有者 BL 的单个旧位置同时持有
// A、B 两个签名范围，迁移目标拆成两个各管一个范围的影子位置。
func sharedSplitChip() *spec.Chip {
	return &spec.Chip{
		Owners: []spec.OwnerID{"BL"},
		Positions: []spec.Position{
			{ID: "legacy", Owner: "BL", Scopes: []spec.Scope{"A", "B"}, Public: true},
			{ID: "shadow-a", Owner: "BL", Scopes: []spec.Scope{"A"}, Shadow: true, Public: true},
			{ID: "shadow-b", Owner: "BL", Scopes: []spec.Scope{"B"}, Shadow: true, Public: true},
		},
		Images: map[string]spec.Image{
			// 撤销映像 SV0：计数升到代次 1 后必须在所有路径上被拒绝。
			"revoked": {ID: "revoked", Versions: map[spec.Scope]spec.Generation{"A": 0, "B": 0}},
			// 恢复映像 SV2：允许计数烧到 1 完成撤销，同时保证恢复组合可启动。
			"recovery": {ID: "recovery", Versions: map[spec.Scope]spec.Generation{"A": 2, "B": 2}},
		},
		Revoked:  map[spec.OwnerID]string{"BL": "revoked"},
		Recovery: []spec.RecoveryCombo{{Images: map[spec.OwnerID]string{"BL": "recovery"}}},
		Initial: &spec.State{
			Counters: map[spec.PositionID]map[spec.Scope]spec.Generation{
				// 起点：旧位置已烧到代次 1，SV0 已被撤销；恢复映像 SV2 仍可启动。
				"legacy":   {"A": 1, "B": 1},
				"shadow-a": {"A": 0},
				"shadow-b": {"B": 0},
			},
			Pointers: map[spec.OwnerID]spec.Pointer{"BL": {Phase: spec.PhaseStable}},
		},
		MaxBurns: 2,
	}
}

func TestSharedPositionSplit(t *testing.T) {
	chip := sharedSplitChip()
	planner, _, report := verifyPlanner(t, chip)

	// 完整枚举必须同时包含稳定、pending（旧/影子都可读）与已确认信念。
	if planner.Nodes() < 3 {
		t.Fatalf("信念数量过少: %d", planner.Nodes())
	}
	if report.Beliefs != planner.Nodes() {
		t.Fatalf("核验器与规划器信念数不一致: %d vs %d", report.Beliefs, planner.Nodes())
	}

	// 正确顺序（每一步都过安全检查）：
	//  1. 两个影子位置分别暂存代次 1（2 次），旧路径继续靠代次 1 拒绝 SV0；
	//  2. request 进入 pending：旧/影子路径都拒绝 SV0、都接受 SV2 恢复；
	//  3. confirm 切到影子，迁移完成。
	// 影子 A、B 各烧一次，最坏烧录次数 = 2。
	if got := planner.WorstCaseBurns(); got != 2 {
		t.Fatalf("共享分裂最坏烧录次数 = %d, 期望 2", got)
	}
}
