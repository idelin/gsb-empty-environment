package migrationtest

import (
	"encoding/json"
	"testing"

	"prescreen.local/ddd-10554-v3/migration"
	"prescreen.local/ddd-10554-v3/spec"
	"prescreen.local/ddd-10554-v3/verifier"
)

// TestCertificateJSONRoundTrip 验证证书可序列化为 JSON 并被独立核验器接受，
// 模拟宿主把证书落盘后由另一个可信组件离线核验的流程。
func TestCertificateJSONRoundTrip(t *testing.T) {
	for _, mk := range []func() *spec.Chip{
		sharedSplitChip,
		pendingChip,
		observabilityChip,
	} {
		chip := mk()
		planner, err := migration.Solve(chip)
		if err != nil {
			t.Fatalf("规划失败: %v", err)
		}
		cert, err := planner.BuildCertificate()
		if err != nil {
			t.Fatalf("证书生成失败: %v", err)
		}

		raw, err := json.Marshal(cert)
		if err != nil {
			t.Fatalf("证书序列化失败: %v", err)
		}
		var decoded spec.Certificate
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("证书反序列化失败: %v", err)
		}

		report := verifier.Verify(&decoded)
		if !report.OK() {
			t.Fatalf("往返后证书核验失败: %v", report.Errors)
		}
		if report.WorstCaseBurns != cert.WorstCaseBurns {
			t.Fatalf("往返后最坏烧录次数漂移: %d vs %d",
				report.WorstCaseBurns, cert.WorstCaseBurns)
		}

		// 独立核验的信念数与具体状态数必须和规划器枚举一致。
		if report.Beliefs != planner.Nodes() {
			t.Fatalf("信念数不一致: verifier=%d planner=%d",
				report.Beliefs, planner.Nodes())
		}
		if report.ConcreteStates != planner.ConcreteStates() {
			t.Fatalf("具体状态数不一致: verifier=%d planner=%d",
				report.ConcreteStates, planner.ConcreteStates())
		}
	}
}

// TestPolicyIsObservational 显式验证：同一信念（可能含多个内部状态）
// 在每个预算格点只对应一个动作，且该动作在信念的每个相容状态上都合法。
func TestPolicyIsObservational(t *testing.T) {
	chip := observabilityChip()
	_, cert, report := verifyPlanner(t, chip)

	root := cert.Beliefs[cert.RootBeliefKey]
	if len(root.States) < 2 {
		t.Fatalf("根信念应包含至少两个不可区分内部状态，实际 %d", len(root.States))
	}

	seen := map[string]map[int]string{}
	for _, row := range cert.Policy {
		if seen[row.BeliefKey] == nil {
			seen[row.BeliefKey] = map[int]string{}
		}
		if existing, ok := seen[row.BeliefKey][row.BudgetLeft]; ok && existing != row.ActionKey {
			t.Fatalf("信念 %s 预算 %d 出现两个不同动作，违反观测一致性",
				row.BeliefKey, row.BudgetLeft)
		}
		seen[row.BeliefKey][row.BudgetLeft] = row.ActionKey
	}
	if !report.Checks["policy_optimal_and_observational"] {
		t.Fatal("独立核验器未确认策略的观测一致性")
	}
}
